package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Backup blocks writers while copying the committed prefix. A sidecar catches truncation.
func (e *Engine) Backup(dst string) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed || e.poisoned {
		return ErrStorage
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	manifestCreated := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(dst)
			if manifestCreated {
				os.Remove(dst + ".sha256")
			}
		}
	}()
	st, err := e.file.Stat()
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, err = io.Copy(io.MultiWriter(f, hash), io.NewSectionReader(e.file, 0, st.Size())); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	mf, err := os.OpenFile(dst+".sha256", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	manifestCreated = true
	_, err = mf.WriteString(hex.EncodeToString(hash.Sum(nil)) + "\n")
	if err == nil {
		err = mf.Sync()
	}
	mf.Close()
	if err != nil {
		return err
	}
	if err = syncDir(filepath.Dir(dst)); err != nil {
		return err
	}
	ok = true
	return nil
}
func Restore(src, dst string) error {
	manifest, err := os.ReadFile(src + ".sha256")
	if err != nil {
		return err
	}
	expected := strings.TrimSpace(string(manifest))
	if len(expected) != 64 {
		return errors.New("invalid backup checksum")
	}
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		target.Close()
		if !ok {
			os.Remove(dst)
		}
	}()
	hash := sha256.New()
	if _, err = io.Copy(io.MultiWriter(target, hash), source); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("backup checksum mismatch")
	}
	if err = target.Sync(); err != nil {
		return err
	}
	if err = target.Close(); err != nil {
		return err
	}
	e, err := Open(dst)
	if err != nil {
		return err
	}
	e.Close()
	ok = true
	return syncDir(filepath.Dir(dst))
}

// keep=0 preserves all revisions. Receipt records and tombstones are retained.
func (e *Engine) Compact(keep int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.poisoned {
		return ErrStorage
	}
	if keep < 0 {
		return fmt.Errorf("keep must be nonnegative")
	}
	next := map[string][]Record{}
	for id, h := range e.records {
		if keep > 0 && len(h) > keep {
			h = h[len(h)-keep:]
		}
		next[id] = h
	}
	return e.rewriteLocked(next, e.receipts)
}

// snapshotChunk bounds one snapshot frame well under maxFrame, so a snapshot of any size is
// a sequence of frames rather than one frame that stops fitting at a few tens of thousands
// of records.
var snapshotChunk = 8 << 20

// snapshotFrames encodes records (ids sorted, each history oldest first) and receipts as
// snapshot frames of at most snapshotChunk bytes of JSON each. The first frame always exists,
// even for an empty database, because it is the one that resets state on replay.
func snapshotFrames(records map[string][]Record, receipts map[string]Receipt, sequence uint64) ([][]byte, error) {
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	frames := [][]byte{}
	event := Event{Format: 2, Snapshot: true, Sequence: sequence}
	size := 0
	flush := func() error {
		b, err := frame(event)
		if err != nil {
			return err
		}
		frames = append(frames, b)
		event = Event{Format: 2, Snapshot: true, Sequence: sequence}
		size = 0
		return nil
	}
	for _, id := range ids {
		for _, r := range records[id] {
			b, err := json.Marshal(r)
			if err != nil {
				return nil, err
			}
			if size > 0 && size+len(b) > snapshotChunk {
				if err = flush(); err != nil {
					return nil, err
				}
			}
			event.Records = append(event.Records, r)
			size += len(b) + 1
		}
	}
	keys := make([]string, 0, len(receipts))
	for k := range receipts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, err := json.Marshal(receipts[k])
		if err != nil {
			return nil, err
		}
		if size > 0 && size+len(b)+len(k) > snapshotChunk {
			if err = flush(); err != nil {
				return nil, err
			}
		}
		if event.Receipts == nil {
			event.Receipts = map[string]Receipt{}
		}
		event.Receipts[k] = receipts[k]
		size += len(b) + len(k) + 4
	}
	if size > 0 || len(frames) == 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return frames, nil
}

// rewriteLocked replaces the journal with a snapshot of next and receipts, atomically: the
// snapshot is written and fsynced beside the journal, renamed over it, and only then does the
// engine switch to it. The caller holds the write lock.
func (e *Engine) rewriteLocked(next map[string][]Record, receipts map[string]Receipt) error {
	frames, err := snapshotFrames(next, receipts, e.sequence)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(e.path), ".memorydb-snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	for _, b := range frames {
		if _, err = f.Write(b); err != nil {
			return err
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), e.path); err != nil {
		return err
	}
	if err = syncDir(filepath.Dir(e.path)); err != nil {
		e.poisoned = true
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	newFile, err := os.OpenFile(e.path, os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		e.poisoned = true
		return ErrStorage
	}
	old := e.file
	e.file = newFile
	e.writeFrame = newFile.Write
	e.syncFile = newFile.Sync
	e.records = next
	e.receipts = receipts
	e.rebuildIndexes()
	old.Close()
	return nil
}

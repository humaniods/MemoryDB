package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	ids := make([]string, 0, len(e.records))
	for id := range e.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rs := []Record{}
	next := map[string][]Record{}
	for _, id := range ids {
		h := e.records[id]
		if keep > 0 && len(h) > keep {
			h = h[len(h)-keep:]
		}
		next[id] = h
		rs = append(rs, h...)
	}
	b, err := frame(Event{Format: 2, Snapshot: true, Records: rs, Receipts: e.receipts, Sequence: e.sequence})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(e.path), ".memorydb-snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return err
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
	e.rebuildIndexes()
	old.Close()
	return nil
}

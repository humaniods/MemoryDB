package main

import (
	"encoding/binary"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
)

// journalFrames counts the frames in a journal and how many of them are snapshot frames.
func journalFrames(t *testing.T, path string) (total, snapshots int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for len(b) >= 4 {
		n := int(binary.BigEndian.Uint32(b[:4]))
		var event Event
		if err = decodeJSON(b[4:4+n], &event); err != nil {
			t.Fatal(err)
		}
		total++
		if event.Snapshot {
			snapshots++
		}
		b = b[4+n+32:]
	}
	return
}

func TestCompactionPastOneFrameRoundTrips(t *testing.T) {
	defer func(old int) { snapshotChunk = old }(snapshotChunk)
	snapshotChunk = 4096
	e, p := openTest(t)
	rng := rand.New(rand.NewSource(11))
	ids := []string{}
	for i := 0; i < 120; i++ {
		r, err := e.Batch([]Change{{benchMemory(rng), 0}}, "key-"+string(rune('a'+i%26))+strings.Repeat("x", i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r[0].ID)
	}
	for _, id := range ids[:40] {
		r, _ := e.Latest(id)
		r.Content = "revised " + r.Content
		if _, err := e.Put(r, r.Version); err != nil {
			t.Fatal(err)
		}
	}
	wantRecords, wantReceipts, wantSeq := e.records, e.receipts, e.Sequence()
	if err := e.Compact(0); err != nil {
		t.Fatal(err)
	}
	total, snaps := journalFrames(t, p)
	if snaps < 2 || total != snaps {
		t.Fatalf("expected a multi-frame snapshot and nothing else, got %d frames, %d snapshot", total, snaps)
	}
	// Writes after a multi-frame snapshot append normally and replay in order.
	extra, err := e.Put(sample("written after compaction"), 0)
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	reopened, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Sequence() != wantSeq+1 {
		t.Fatal("sequence lost across multi-frame snapshot:", reopened.Sequence(), wantSeq+1)
	}
	for id, h := range wantRecords {
		if !reflect.DeepEqual(stripMono(reopened.records[id]), stripMono(h)) {
			t.Fatal("history changed across compaction for", id)
		}
	}
	if _, err = reopened.Latest(extra.ID); err != nil {
		t.Fatal(err)
	}
	if len(reopened.receipts) != len(wantReceipts) {
		t.Fatal("receipts lost across compaction:", len(reopened.receipts), len(wantReceipts))
	}
	// An idempotent retry after compaction still answers from its receipt.
	again, err := reopened.Batch([]Change{{benchMemory(rand.New(rand.NewSource(11))), 0}}, "key-a")
	if err != nil || again[0].ID != ids[0] {
		t.Fatal("idempotent retry did not survive compaction:", err)
	}
	if hits := reopened.Search(Query{User: "u", Workspace: "w", Text: "revised"}); len(hits) != 40 {
		t.Fatal("search after reopen found", len(hits))
	}
}

// stripMono drops the monotonic clock reading so records compare by their stored value.
func stripMono(h []Record) []Record {
	out := make([]Record, len(h))
	for i, r := range h {
		r.Created = r.Created.Round(0)
		out[i] = r
	}
	return out
}

func TestCompactionOfMoreThanOneMaxFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("writes ~40 MB")
	}
	e, p := openTest(t)
	e.syncFile = func() error { return nil }
	rng := rand.New(rand.NewSource(5))
	big := func() Record {
		r := benchMemory(rng)
		r.Vector = make([]float64, 4096)
		for i := range r.Vector {
			r.Vector[i] = rng.Float64()
		}
		r.EmbeddingModel = "big-4096"
		return r
	}
	for i := 0; i < 5; i++ {
		changes := []Change{}
		for j := 0; j < 100; j++ {
			changes = append(changes, Change{big(), 0})
		}
		if _, err := e.Batch(changes, ""); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := os.Stat(p)
	if st.Size() <= maxFrame {
		t.Fatalf("test corpus is %d bytes, not past one frame", st.Size())
	}
	if err := e.Compact(0); err != nil {
		t.Fatal("compaction of a journal larger than one frame failed:", err)
	}
	e.Close()
	reopened, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.records) != 500 {
		t.Fatal("records after reopen:", len(reopened.records))
	}
}

func TestSnapshotFrameAfterTransactionIsRefused(t *testing.T) {
	e, p := openTest(t)
	if _, err := e.Put(sample("ordinary write"), 0); err != nil {
		t.Fatal(err)
	}
	b, err := frame(Event{Format: 2, Snapshot: true, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.writeFrame(b); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if _, err = Open(p); err == nil || !strings.Contains(err.Error(), "snapshot must be first frame") {
		t.Fatal("snapshot frame after a transaction was accepted:", err)
	}
}

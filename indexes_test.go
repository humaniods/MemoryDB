package main

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func codedDoc(code string, tag string) Record {
	return Record{Kind: "document", Collection: "benchdocs", User: "u", Workspace: "w",
		Data: map[string]any{"n": 1, "tag": tag, "code": code}}
}

// assertDerivedStateMatchesRebuild proves the incremental maintenance a batch does leaves
// exactly what deriving everything from scratch would.
func assertDerivedStateMatchesRebuild(t *testing.T, e *Engine) {
	t.Helper()
	// The incremental checks must never admit a state the full validation would refuse.
	if err := validateCollections(e.records, time.Now()); err != nil {
		t.Fatal("incremental validation admitted an invalid state:", err)
	}
	gotIdx, gotUnique, gotTerms, gotSchemas := e.indexes, e.unique, e.terms, e.schemas
	e.rebuildIndexes()
	if !reflect.DeepEqual(gotIdx, e.indexes) {
		t.Fatalf("equality indexes drifted from a rebuild:\n got %v\nwant %v", gotIdx, e.indexes)
	}
	if !reflect.DeepEqual(gotUnique, e.unique) {
		t.Fatalf("unique holders drifted from a rebuild:\n got %v\nwant %v", gotUnique, e.unique)
	}
	if !reflect.DeepEqual(gotTerms, e.terms) {
		t.Fatal("term statistics drifted from a rebuild")
	}
	if !reflect.DeepEqual(gotSchemas, e.schemas) {
		t.Fatal("schema cache drifted from a rebuild")
	}
}

func TestUniqueSwapInsideOneBatch(t *testing.T) {
	e, _ := openTest(t)
	if _, err := e.Put(benchCollection(), 0); err != nil {
		t.Fatal(err)
	}
	a, err := e.Put(codedDoc("A-1", "x"), 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Put(codedDoc("B-1", "x"), 0)
	if err != nil {
		t.Fatal(err)
	}
	// Two rows trade their unique codes in one transaction: legal, because the constraint
	// holds on the state the batch produces, not on the state it started from.
	a.Data["code"], b.Data["code"] = "B-1", "a-1"
	if _, err = e.Batch([]Change{{a, 1}, {b, 1}}, ""); err != nil {
		t.Fatal("swap refused:", err)
	}
	// casefold: "A-1" now collides with b's "a-1".
	if _, err = e.Put(codedDoc("A-1", "y"), 0); !errors.Is(err, ErrConflict) {
		t.Fatal("casefold duplicate accepted after swap:", err)
	}
	// Two new rows claiming one code in the same batch.
	if _, err = e.Batch([]Change{{codedDoc("C-1", "z"), 0}, {codedDoc("c-1", "z"), 0}}, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate within one batch accepted:", err)
	}
	assertDerivedStateMatchesRebuild(t, e)
}

func TestExpiredOrDeletedHolderReleasesUniqueValue(t *testing.T) {
	e, _ := openTest(t)
	if _, err := e.Put(benchCollection(), 0); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	gone := codedDoc("K-1", "x")
	gone.Expires = &past
	if _, err := e.Put(gone, 0); err != nil {
		t.Fatal(err)
	}
	live, err := e.Put(codedDoc("K-1", "x"), 0)
	if err != nil {
		t.Fatal("expired holder still blocks its value:", err)
	}
	if _, err = e.Put(codedDoc("K-1", "x"), 0); !errors.Is(err, ErrConflict) {
		t.Fatal("live holder did not block its value:", err)
	}
	live.Deleted = true
	if _, err = e.Put(live, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Put(codedDoc("K-1", "x"), 0); err != nil {
		t.Fatal("deleted holder still blocks its value:", err)
	}
	assertDerivedStateMatchesRebuild(t, e)
}

func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	e, _ := openTest(t)
	if _, err := e.Put(benchCollection(), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Put(codedDoc("TAKEN", "x"), 0); err != nil {
		t.Fatal(err)
	}
	before := e.Sequence()
	fresh := codedDoc("FRESH", "only-in-rejected-batch")
	fresh.ID = "fresh-row"
	fact := sample("only in the rejected batch")
	if _, err := e.Batch([]Change{{fresh, 0}, {fact, 0}, {codedDoc("taken", "y"), 0}}, ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if e.Sequence() != before {
		t.Fatal("rejected batch moved the sequence")
	}
	if _, err := e.Latest("fresh-row"); err != ErrMissing {
		t.Fatal("rejected batch left a record:", err)
	}
	page, err := e.Documents("u", "w", "benchdocs", DocumentQuery{Where: []Predicate{{"tag", "eq", "only-in-rejected-batch"}}})
	if err != nil || page.Total != 0 {
		t.Fatal("rejected batch left an index entry:", page.Total, err)
	}
	if hits := e.Search(Query{User: "u", Workspace: "w", Text: "rejected"}); len(hits) != 0 {
		t.Fatal("rejected batch is searchable:", hits)
	}
	if _, err = e.Put(fresh, 0); err != nil {
		t.Fatal("rejected batch kept its unique claim:", err)
	}
	assertDerivedStateMatchesRebuild(t, e)
}

func TestFailedJournalWriteRollsBackInPlaceApply(t *testing.T) {
	e, _ := openTest(t)
	kept, err := e.Put(sample("kept as it was"), 0)
	if err != nil {
		t.Fatal(err)
	}
	e.syncFile = func() error { return errors.New("injected sync failure") }
	changed := kept
	changed.Content = "never durable"
	if _, err = e.Batch([]Change{{changed, 1}, {sample("never durable either"), 0}}, ""); !errors.Is(err, ErrStorage) {
		t.Fatal(err)
	}
	if got, _ := e.Latest(kept.ID); got.Version != 1 || got.Content != "kept as it was" {
		t.Fatal("failed write changed a visible record:", got)
	}
	if hits := e.Search(Query{User: "u", Workspace: "w", Text: "durable"}); len(hits) != 0 {
		t.Fatal("failed write is searchable:", hits)
	}
}

func TestIncrementalIndexesUnderRandomWrites(t *testing.T) {
	e, _ := openTest(t)
	if _, err := e.Put(benchCollection(), 0); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(7))
	live := []Record{}
	for i := 0; i < 400; i++ {
		switch op := rng.Intn(4); {
		case op == 0 || len(live) == 0:
			r, err := e.Put(codedDoc(fmt.Sprintf("R-%d", i), fmt.Sprintf("t%d", rng.Intn(5))), 0)
			if err != nil {
				t.Fatal(err)
			}
			live = append(live, r)
		case op == 1:
			r, err := e.Put(benchMemory(rng), 0)
			if err != nil {
				t.Fatal(err)
			}
			live = append(live, r)
		case op == 2:
			j := rng.Intn(len(live))
			r := live[j]
			if r.Kind == "document" {
				r.Data["tag"] = fmt.Sprintf("t%d", rng.Intn(5))
				r.Data["code"] = fmt.Sprintf("U-%d", i)
			} else {
				r.Content = benchSentence(rng)
			}
			got, err := e.Put(r, r.Version)
			if err != nil {
				t.Fatal(err)
			}
			live[j] = got
		default:
			j := rng.Intn(len(live))
			r := live[j]
			r.Deleted = true
			if _, err := e.Put(r, r.Version); err != nil {
				t.Fatal(err)
			}
			live = append(live[:j], live[j+1:]...)
		}
	}
	assertDerivedStateMatchesRebuild(t, e)
}

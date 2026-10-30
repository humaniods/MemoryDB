package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func collection(name string) Record {
	return Record{Kind: "collection", Collection: name, User: "u", Workspace: "w", Content: name, Visibility: "team", Data: map[string]any{"fields": map[string]any{"email": map[string]any{"type": "string", "required": true}, "age": map[string]any{"type": "integer"}}, "unique": []any{"email"}, "indexes": []any{"age"}}}
}
func document(email string, age any) Record {
	return Record{Kind: "document", Collection: "users", User: "u", Workspace: "w", Data: map[string]any{"email": email, "age": age}}
}
func setupDocuments(t *testing.T) (*Engine, string) {
	e, p := openTest(t)
	if _, err := e.Put(collection("users"), 0); err != nil {
		t.Fatal(err)
	}
	return e, p
}
func TestAtomicBatchRollbackAndIdempotency(t *testing.T) {
	e, p := setupDocuments(t)
	rs, err := e.Batch([]Change{{document("a", 20), 0}, {document("b", 30), 0}}, "insert-users")
	if err != nil || len(rs) != 2 {
		t.Fatal(rs, err)
	}
	seq := e.Sequence()
	again, err := e.Batch([]Change{{document("a", 20), 0}, {document("b", 30), 0}}, "insert-users")
	if err != nil || again[0].ID != rs[0].ID || e.Sequence() != seq {
		t.Fatal("idempotency failed", err)
	}
	if _, err = e.Batch([]Change{{document("c", 10), 0}, {document("a", 40), 0}}, ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	page, _ := e.Documents("u", "w", "users", DocumentQuery{})
	if page.Total != 2 || e.Sequence() != seq {
		t.Fatal("failed transaction changed state")
	}
	e.Close()
	e, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	again, err = e.Batch([]Change{{document("a", 20), 0}, {document("b", 30), 0}}, "insert-users")
	if err != nil || again[0].ID != rs[0].ID {
		t.Fatal("receipt lost after restart", err)
	}
}
func TestCollectionSchemaAndDeletionConstraints(t *testing.T) {
	e, _ := setupDocuments(t)
	r, err := e.Put(document("a", 20), 0)
	if err != nil {
		t.Fatal(err)
	}
	bad := document("b", "twenty")
	if _, err = e.Put(bad, 0); err == nil {
		t.Fatal("schema violation accepted")
	}
	schemas := e.Collections("w")
	schema := schemas[0]
	schema.Deleted = true
	if _, err = e.Put(schema, schema.Version); err == nil {
		t.Fatal("collection dropped with live documents")
	}
	r.Deleted = true
	if _, err = e.Batch([]Change{{r, r.Version}, {schema, schema.Version}}, ""); err != nil {
		t.Fatal("atomic drop failed", err)
	}
}
func TestQueryIndexesPaginationAndExactNumbers(t *testing.T) {
	e, _ := setupDocuments(t)
	for i, a := range []json.Number{"9007199254740993", "9007199254740992", "7"} {
		_, err := e.Put(document(string(rune('a'+i)), a), 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	q := DocumentQuery{Where: []Predicate{{"age", "eq", json.Number("9007199254740993")}}}
	p, err := e.Documents("u", "w", "users", q)
	if err != nil || p.Total != 1 || !strings.Contains(p.Plan, "index") {
		t.Fatal(p, err)
	}
	if p.Results[0].Data["age"].(json.Number) != "9007199254740993" {
		t.Fatal("integer precision lost")
	}
	q = DocumentQuery{OrderBy: Order{{Field: "age"}}, Limit: 1}
	p, err = e.Documents("u", "w", "users", q)
	if err != nil || p.Results[0].Data["age"].(json.Number) != "7" || p.Next == "" {
		t.Fatal(p, err)
	}
	q.Cursor = p.Next
	p2, err := e.Documents("u", "w", "users", q)
	if err != nil || p2.Results[0].Data["age"].(json.Number) != "9007199254740992" {
		t.Fatal(p2, err)
	}
	e.Put(document("d", 8), 0)
	if _, err = e.Documents("u", "w", "users", q); !errors.Is(err, ErrConflict) {
		t.Fatal("stale cursor accepted", err)
	}
	if _, err = e.Documents("u", "w", "users", DocumentQuery{Where: []Predicate{{"age", "exists", "yes"}}}); err == nil {
		t.Fatal("invalid predicate accepted")
	}
}
func TestOrNotAndCaseInsensitiveQueries(t *testing.T) {
	e, _ := setupDocuments(t)
	// age carries the view count here; name is undeclared, allowed because the schema is not strict.
	member := func(email, name string, views json.Number) Record {
		r := document(email, views)
		r.Data["name"] = name
		return r
	}
	for _, r := range []Record{
		member("a@x.com", "Univorya", "7"),
		member("b@x.com", "Memories", "3"),
		member("c@x.com", "Other", "1"),
	} {
		if _, err := e.Put(r, 0); err != nil {
			t.Fatal(err)
		}
	}
	// Login lookup: any one field can identify the account.
	page, err := e.Documents("u", "w", "users", DocumentQuery{Or: [][]Predicate{
		{{"email", "eq", "b@x.com"}}, {{"name", "eq", "Memories"}},
	}})
	if err != nil || page.Total != 1 || page.Results[0].Data["email"] != "b@x.com" {
		t.Fatal(page, err)
	}
	// Search: free text across fields, still narrowed by the ANDed Where clause.
	page, err = e.Documents("u", "w", "users", DocumentQuery{
		Where: []Predicate{{"age", "gte", json.Number("3")}},
		Or:    [][]Predicate{{{"name", "contains", "emo"}}, {{"name", "contains", "niv"}}},
	})
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
	page, err = e.Documents("u", "w", "users", DocumentQuery{Not: []Predicate{{"email", "eq", "a@x.com"}}})
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
	page, err = e.Documents("u", "w", "users", DocumentQuery{Where: []Predicate{{"email", "iequals", "A@X.com"}}})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	if err := validateQuery(DocumentQuery{Or: [][]Predicate{{}}}); err == nil {
		t.Fatal("empty or group accepted")
	}
	if err := validateQuery(DocumentQuery{Or: [][]Predicate{{{"", "eq", "x"}}}}); err == nil {
		t.Fatal("or group with empty field accepted")
	}
	if err := validateQuery(DocumentQuery{Not: []Predicate{{"email", "nope", "x"}}}); err == nil {
		t.Fatal("unknown operator in not accepted")
	}
}

func TestCaseInsensitiveContainsMatchesSQLiteLike(t *testing.T) {
	data := map[string]any{"title": "UniVorya Platform", "tags": []any{"Python", "FastAPI"}}
	// SQLite LIKE is case-insensitive for ASCII, so platform search must be too.
	if !matches(data, []Predicate{{"title", "icontains", "univory"}}) {
		t.Fatal("icontains should fold case on strings")
	}
	if !matches(data, []Predicate{{"title", "contains", "Vory"}}) {
		t.Fatal("contains lost exact substring behaviour")
	}
	if matches(data, []Predicate{{"title", "contains", "univory"}}) {
		t.Fatal("contains became case-insensitive")
	}
	if !matches(data, []Predicate{{"tags", "icontains", "fastapi"}}) {
		t.Fatal("icontains should fold case on array members")
	}
	if !matches(data, []Predicate{{"tags", "contains", "Python"}}) {
		t.Fatal("array membership lost")
	}
	if matches(data, []Predicate{{"tags", "contains", "pyth"}}) {
		t.Fatal("array contains became substring")
	}
	if matches(data, []Predicate{{"missing", "icontains", "x"}}) {
		t.Fatal("icontains matched absent field")
	}
}
func TestAggregationsCoverFullFilteredSet(t *testing.T) {
	e, _ := setupDocuments(t)
	seed := []Record{
		document("a@x.com", json.Number("10")),
		document("b@x.com", json.Number("20")),
		document("c@x.com", json.Number("30")),
		document("d@x.com", nil),
		document("e@x.com", json.Number("9007199254740993")),
	}
	for i, r := range seed {
		r.Data["team"] = []string{"alpha", "alpha", "beta", "delta", "gamma"}[i]
		if r.Data["age"] == nil {
			delete(r.Data, "age")
		}
		if _, err := e.Put(r, 0); err != nil {
			t.Fatal(err)
		}
	}
	total := DocumentQuery{Aggregate: []Aggregation{
		{Fn: "sum", Field: "age"}, {Fn: "avg", Field: "age"},
		{Fn: "min", Field: "age"}, {Fn: "max", Field: "age"},
	}}
	page, err := e.Documents("u", "w", "users", total)
	if err != nil || len(page.Groups) != 1 {
		t.Fatal(page, err)
	}
	got := page.Groups[0].Values
	// 10+20+30+2^53+1 must not round through a float64.
	if got["sum_age"] != json.Number("9007199254741053") {
		t.Fatal("sum lost precision:", got["sum_age"])
	}
	// An exact quarter stays exact rather than becoming 2251799813685263.2.
	if got["avg_age"] != json.Number("2251799813685263.25") {
		t.Fatal("avg rounded:", got["avg_age"])
	}
	if got["min_age"] != json.Number("10") || got["max_age"] != json.Number("9007199254740993") {
		t.Fatal("min/max wrong:", got)
	}
	if page.Groups[0].Count != 5 {
		t.Fatal("count should cover all five records:", page.Groups[0].Count)
	}

	// A page of one still aggregates every matching record.
	page, err = e.Documents("u", "w", "users", DocumentQuery{
		Aggregate: []Aggregation{{Fn: "sum", Field: "age"}}, Limit: 1,
	})
	if err != nil || len(page.Results) != 1 || page.Groups[0].Values["sum_age"] != json.Number("9007199254741053") {
		t.Fatal("pagination skewed the total", page, err)
	}

	grouped, err := e.Documents("u", "w", "users", DocumentQuery{
		GroupBy:   []string{"team"},
		Aggregate: []Aggregation{{Fn: "sum", Field: "age"}, {Fn: "max", Field: "age", As: "top"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Groups) != 4 {
		t.Fatalf("expected four groups, got %d", len(grouped.Groups))
	}
	for i, want := range []string{"alpha", "beta", "delta", "gamma"} {
		g := grouped.Groups[i]
		if g.Group["team"] != want {
			t.Fatalf("groups not ordered deterministically: %v", grouped.Groups)
		}
		if want == "delta" {
			// SQL SUM over rows with no value is NULL, not zero.
			if g.Count != 1 || g.Values["sum_age"] != nil {
				t.Fatal("empty group should report null:", g)
			}
			continue
		}
		if g.Values["sum_age"] == nil {
			t.Fatal("group sum missing:", g)
		}
	}
	if grouped.Groups[0].Values["top"] != json.Number("20") {
		t.Fatal("as alias not applied:", grouped.Groups[0])
	}

	// Narrower filters must narrow the aggregate too.
	narrow, err := e.Documents("u", "w", "users", DocumentQuery{
		Where:     []Predicate{{"team", "eq", "alpha"}},
		Aggregate: []Aggregation{{Fn: "sum", Field: "age"}},
	})
	if err != nil || narrow.Groups[0].Values["sum_age"] != json.Number("30") {
		t.Fatal("aggregate ignored where:", narrow, err)
	}

	for _, bad := range []DocumentQuery{
		{Aggregate: []Aggregation{{Fn: "median", Field: "age"}}},
		{Aggregate: []Aggregation{{Fn: "sum", Field: ""}}},
		{Aggregate: []Aggregation{{Fn: "sum", Field: "age"}, {Fn: "sum", Field: "age"}}},
		{Aggregate: []Aggregation{{Fn: "sum", Field: "age", As: "9bad"}}},
		{GroupBy: []string{""}},
	} {
		if _, err := e.Documents("u", "w", "users", bad); err == nil {
			t.Fatal("invalid aggregate accepted:", bad)
		}
	}
	if _, err := e.Documents("u", "w", "users", DocumentQuery{}); err != nil {
		t.Fatal("plain query broke:", err)
	}
	page, _ = e.Documents("u", "w", "users", DocumentQuery{})
	if page.Groups != nil {
		t.Fatal("plain query returned aggregates")
	}
}
func TestAtomicIncrementSurvivesConcurrency(t *testing.T) {
	e, _ := setupDocuments(t)
	// views is undeclared, so this also covers incrementing a field a record lacks:
	// it has to start from zero rather than fail.
	created, err := e.Put(document("a@x.com", json.Number("1")), 0)
	if err != nil {
		t.Fatal(err)
	}
	const viewers = 64
	var wg sync.WaitGroup
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Mutate(created.ID, nil, nil, map[string]any{"views": json.Number("1")}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	page, err := e.Documents("u", "w", "users", DocumentQuery{Where: []Predicate{{"email", "eq", "a@x.com"}}})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	got := page.Results[0].Data
	// Read-modify-write from the client would drop most of these; nobody may be lost.
	if got["views"] != json.Number(fmt.Sprint(viewers)) {
		t.Fatal("lost updates:", got["views"])
	}
	if got["age"] != json.Number("1") {
		t.Fatal("increment disturbed another field:", got)
	}
	if page.Results[0].Version != viewers+1 {
		t.Fatal("version did not advance once per write:", page.Results[0].Version)
	}

	// Negative steps, big exact integers, and a declared numeric field.
	if _, err = e.Mutate(created.ID, nil, nil, map[string]any{"views": json.Number("-4"), "age": json.Number("2")}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Mutate(created.ID, nil, nil, map[string]any{"age": json.Number("9007199254740991")}); err != nil {
		t.Fatal(err)
	}
	page, _ = e.Documents("u", "w", "users", DocumentQuery{Where: []Predicate{{"email", "eq", "a@x.com"}}})
	got = page.Results[0].Data
	if got["views"] != json.Number("60") || got["age"] != json.Number("9007199254740994") {
		t.Fatal("inc wrong:", got)
	}

	// Refusing to add onto text, and reporting the missing record honestly.
	if _, err = e.Mutate(created.ID, nil, nil, map[string]any{"email": json.Number("1")}); err == nil {
		t.Fatal("increment onto a string accepted")
	}
	if _, err = e.Mutate(created.ID, nil, nil, map[string]any{"views": "3"}); err == nil {
		t.Fatal("non-numeric step accepted")
	}
	if _, err = e.Mutate("00000000000000000000000000000000", nil, nil, map[string]any{"views": json.Number("1")}); !errors.Is(err, ErrMissing) {
		t.Fatal("unknown id:", err)
	}
	// set and unset still ride along in the same locked write.
	if _, err = e.Mutate(created.ID, map[string]any{"stage": "Beta"}, []string{"views"}, nil); err != nil {
		t.Fatal(err)
	}
	page, _ = e.Documents("u", "w", "users", DocumentQuery{Where: []Predicate{{"email", "eq", "a@x.com"}}})
	got = page.Results[0].Data
	if got["stage"] != "Beta" {
		t.Fatal("set not applied:", got)
	}
	if _, present := got["views"]; present {
		t.Fatal("unset not applied:", got)
	}
}
func TestCaseInsensitiveUniqueFields(t *testing.T) {
	e, _ := openTest(t)
	acc := Record{Kind: "collection", Collection: "accounts", User: "u", Workspace: "w", Content: "accounts", Visibility: "team",
		Data: map[string]any{"fields": map[string]any{
			"email":  map[string]any{"type": "string", "required": true, "casefold": true},
			"handle": map[string]any{"type": "string", "required": true},
		}, "unique": []any{"email", "handle"}}}
	if _, err := e.Put(acc, 0); err != nil {
		t.Fatal(err)
	}
	mk := func(email, handle string) Record {
		return Record{Kind: "document", Collection: "accounts", User: "u", Workspace: "w",
			Data: map[string]any{"email": email, "handle": handle}}
	}
	if _, err := e.Put(mk("Priya@x.com", "priya"), 0); err != nil {
		t.Fatal(err)
	}
	// The same identity in other casing must not open a second account.
	if _, err := e.Put(mk("PRIYA@x.com", "other"), 0); !errors.Is(err, ErrConflict) {
		t.Fatal("casefold unique not enforced:", err)
	}
	// A field without casefold keeps SQLite's case-exact unique behaviour.
	if _, err := e.Put(mk("a@x.com", "Priya"), 0); err != nil {
		t.Fatal("case-sensitive unique wrongly rejected a new handle:", err)
	}
	badSchema := Record{Kind: "collection", Collection: "badcols", User: "u", Workspace: "w", Content: "badcols", Visibility: "team",
		Data: map[string]any{"fields": map[string]any{"n": map[string]any{"type": "integer", "casefold": true}}, "unique": []any{"n"}}}
	if _, err := e.Put(badSchema, 0); err == nil {
		t.Fatal("casefold on a non-string field accepted")
	}
	page, err := e.Documents("u", "w", "accounts", DocumentQuery{})
	if err != nil || page.Total != 2 {
		t.Fatal("rejected write left a record behind:", page, err)
	}
	// Folding affects uniqueness only; matching stays explicit about which operator folds.
	page, err = e.Documents("u", "w", "accounts", DocumentQuery{Where: []Predicate{{"email", "iequals", "priya@x.com"}}})
	if err != nil || page.Total != 1 {
		t.Fatal("iequals did not find the folded account:", page, err)
	}
	page, _ = e.Documents("u", "w", "accounts", DocumentQuery{Where: []Predicate{{"email", "eq", "priya@x.com"}}})
	if page.Total != 0 {
		t.Fatal("eq became case-insensitive")
	}
}
func TestMultiColumnSortAndOffsetPaging(t *testing.T) {
	e, _ := setupDocuments(t)
	// The listing sort univorya needs: most-liked, then most-viewed, then newest.
	seed := []struct {
		email        string
		likes, views string
		created      string
	}{
		{"a@x.com", "5", "100", "2026-01-01"},
		{"b@x.com", "5", "100", "2026-02-01"},
		{"c@x.com", "5", "90", "2026-03-01"},
		{"d@x.com", "9", "1", "2026-04-01"},
		{"e@x.com", "0", "500", "2026-05-01"},
	}
	ids := map[string]string{}
	for _, r := range seed {
		rec := document(r.email, nil)
		delete(rec.Data, "age")
		rec.Data["likes"] = json.Number(r.likes)
		rec.Data["views"] = json.Number(r.views)
		rec.Data["created"] = r.created
		created, err := e.Put(rec, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids[r.email] = created.ID
	}
	trending := Order{
		{Field: "likes", Descending: true},
		{Field: "views", Descending: true},
		{Field: "created", Descending: true},
	}
	pages := [][]string{{"d@x.com", "b@x.com"}, {"a@x.com", "c@x.com"}, {"e@x.com"}}
	for page, want := range pages {
		got, err := e.Documents("u", "w", "users", DocumentQuery{OrderBy: trending, Limit: 2, Offset: page * 2})
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 5 {
			t.Fatal("offset changed the total:", got.Total)
		}
		if len(got.Results) != len(want) {
			t.Fatalf("page %d has %d rows, want %d", page, len(got.Results), len(want))
		}
		for i, r := range got.Results {
			if r.Data["email"] != want[i] {
				t.Fatalf("page %d order: got %v want %v", page, got.Results, want)
			}
		}
	}
	// A view landing between page fetches must not knock the reader off page 3. A cursor
	// would refuse the very same request with 409, which is why the UI pages by offset.
	if _, err := e.Mutate(ids["d@x.com"], nil, nil, map[string]any{"views": json.Number("1")}); err != nil {
		t.Fatal(err)
	}
	after, err := e.Documents("u", "w", "users", DocumentQuery{OrderBy: trending, Limit: 2, Offset: 2})
	if err != nil || len(after.Results) != 2 || after.Results[0].Data["email"] != "a@x.com" {
		t.Fatal("offset paging broke after a write", after, err)
	}
	// A page number past the last row answers "nothing", the way LIMIT/OFFSET does in
	// Postgres and SQLite; the total still describes the whole filtered set.
	past, err := e.Documents("u", "w", "users", DocumentQuery{OrderBy: trending, Limit: 2, Offset: 5})
	if err != nil || len(past.Results) != 0 || past.Total != 5 || past.Next != "" {
		t.Fatal("offset beyond the last row should be an empty page", past, err)
	}
	first, err := e.Documents("u", "w", "users", DocumentQuery{OrderBy: trending, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Documents("u", "w", "users", DocumentQuery{OrderBy: trending, Limit: 2, Cursor: first.Next}); err != nil {
		t.Fatal("cursor stopped working", err)
	}

	for _, bad := range []DocumentQuery{
		{Limit: 2, Offset: 2, Cursor: "anything"},
		{OrderBy: Order{{Field: "likes"}, {Field: "views"}, {Field: "created"}, {Field: "email"}, {Field: "likes"}}},
		{OrderBy: Order{{Field: "likes", Descending: true}, {Field: "views"}}, Descending: true},
		{OrderBy: Order{{Field: "likes"}, {Field: "likes"}}},
		{OrderBy: Order{{Field: ""}}},
		{Offset: -1},
		{Aggregate: []Aggregation{{Fn: "nope", Field: "views"}}},
	} {
		if _, err := e.Documents("u", "w", "users", bad); err == nil {
			t.Fatal("invalid query accepted:", bad)
		}
	}
	// The string form keeps its old meaning, descending included.
	newest, err := e.Documents("u", "w", "users", DocumentQuery{OrderBy: Order{{Field: "created"}}, Descending: true, Limit: 1})
	if err != nil || len(newest.Results) != 1 || newest.Results[0].Data["email"] != "e@x.com" {
		t.Fatal("legacy single-field descending sort broke", newest, err)
	}
}
func TestMissingNullAndPredicateTypes(t *testing.T) {
	if matches(map[string]any{}, []Predicate{{"missing", "eq", nil}}) {
		t.Fatal("missing conflated with null")
	}
	if !matches(map[string]any{"x": nil}, []Predicate{{"x", "eq", nil}}) {
		t.Fatal("null compare failed")
	}
	if matches(map[string]any{"x": "20"}, []Predicate{{"x", "gt", json.Number("10")}}) {
		t.Fatal("implicit coercion")
	}
	if _, ok := number(json.Number("1e99999999")); ok {
		t.Fatal("unbounded exponent accepted")
	}
}
func TestCompactionBackupRestoreAndOwnership(t *testing.T) {
	e, p := setupDocuments(t)
	r, _ := e.Put(document("a", 1), 0)
	for i := 2; i <= 4; i++ {
		r.Data["age"] = i
		r, _ = e.Put(r, r.Version)
	}
	if err := e.Compact(1); err != nil {
		t.Fatal(err)
	}
	if len(e.History(r.ID)) != 1 || e.History(r.ID)[0].Version != 4 {
		t.Fatal("compaction changed versions")
	}
	if other, err := Open(p); err == nil {
		other.Close()
		t.Fatal("lock lost after rename")
	}
	r.Data["age"] = 5
	r, err := e.Put(r, 4)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.wal")
	if err = e.Backup(backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored.wal")
	if err = Restore(backup, restored); err != nil {
		t.Fatal(err)
	}
	re, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer re.Close()
	if got, err := re.Latest(r.ID); err != nil || got.Version != 5 {
		t.Fatal(got, err)
	}
	if err = Restore(backup, restored); err == nil {
		t.Fatal("restore overwrote existing database")
	}
	b, _ := os.ReadFile(backup)
	os.WriteFile(backup, b[:len(b)-1], 0600)
	if err = Restore(backup, filepath.Join(t.TempDir(), "bad.wal")); err == nil {
		t.Fatal("truncated backup restored")
	}
	e.Close()
	reopened, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, _ := reopened.Latest(r.ID); got.Version != 5 {
		t.Fatal(got)
	}
}
func TestPartialTransactionRecoveryAndPoisoning(t *testing.T) {
	e, p := setupDocuments(t)
	realWrite := e.writeFrame
	e.writeFrame = func(b []byte) (int, error) { n, _ := realWrite(b[:len(b)/2]); return n, io.ErrShortWrite }
	if _, err := e.Batch([]Change{{document("a", 1), 0}, {document("b", 2), 0}}, ""); !errors.Is(err, ErrStorage) {
		t.Fatal(err)
	}
	if _, err := e.Put(document("c", 3), 0); !errors.Is(err, ErrStorage) {
		t.Fatal("writer continued after failure")
	}
	e.Close()
	e, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	page, _ := e.Documents("u", "w", "users", DocumentQuery{})
	if page.Total != 0 {
		t.Fatal("partial transaction visible")
	}
}
func TestSyncFailureHasRecoverableUnknownOutcome(t *testing.T) {
	e, p := setupDocuments(t)
	e.syncFile = func() error { return errors.New("injected disk sync failure") }
	cs := []Change{{document("a", 1), 0}}
	if _, err := e.Batch(cs, "once"); !errors.Is(err, ErrStorage) {
		t.Fatal(err)
	}
	page, _ := e.Documents("u", "w", "users", DocumentQuery{})
	if page.Total != 0 {
		t.Fatal("unacknowledged state published")
	}
	e.Close()
	e, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	rs, err := e.Batch(cs, "once")
	if err != nil || len(rs) != 1 {
		t.Fatal(err)
	}
	page, _ = e.Documents("u", "w", "users", DocumentQuery{})
	if page.Total != 1 {
		t.Fatal("retry duplicated commit")
	}
}
func TestConcurrentUniqueConstraint(t *testing.T) {
	e, _ := setupDocuments(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.Put(document("same", 1), 0)
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatal(success)
	}
}
func call(s *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if body != nil {
		json.NewEncoder(&b).Encode(body)
	}
	r := httptest.NewRequest(method, path, &b)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func principal(token, user, workspace, role string) Principal {
	hash := sha256.Sum256([]byte(token))
	return Principal{hex.EncodeToString(hash[:]), user, workspace, role}
}
func TestMultiTenantRolesAndHistoryIsolation(t *testing.T) {
	e, _ := openTest(t)
	s := &Server{engine: e, principals: []Principal{principal("alice-secret-token", "alice", "one", "admin"), principal("bob-secret-token", "bob", "one", "writer"), principal("reader-secret-token", "reader", "one", "reader"), principal("other-secret-token", "other", "two", "admin")}}
	w := call(s, "POST", "/api/v1/memories/", "alice-secret-token", map[string]any{"kind": "fact", "content": "private secret"})
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var rec Record
	json.Unmarshal(w.Body.Bytes(), &rec)
	for _, tok := range []string{"bob-secret-token", "other-secret-token"} {
		if call(s, "GET", "/api/v1/memories/"+rec.ID+"/", tok, nil).Code != 404 {
			t.Fatal("private record leaked")
		}
	}
	rec.Content = "public to team"
	rec.Visibility = "team"
	w = call(s, "PUT", "/api/v1/memories/"+rec.ID+"/", "alice-secret-token", rec)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if call(s, "GET", "/api/v1/memories/"+rec.ID+"/", "bob-secret-token", nil).Code != 200 {
		t.Fatal("team read denied")
	}
	if call(s, "GET", "/api/v1/memories/"+rec.ID+"/history/", "bob-secret-token", nil).Code != 403 {
		t.Fatal("old private history leaked")
	}
	if call(s, "POST", "/api/v1/memories/", "reader-secret-token", map[string]any{"kind": "fact", "content": "write"}).Code != 403 {
		t.Fatal("reader wrote")
	}
	if call(s, "GET", "/api/v1/memories/"+rec.ID+"/", "other-secret-token", nil).Code != 404 {
		t.Fatal("tenant leak")
	}
}
func TestIngestEvidenceCorrectionAndRetry(t *testing.T) {
	e, _ := openTest(t)
	s := &Server{engine: e, user: "u", workspace: "w", role: "admin"}
	in := Ingest{Messages: []Message{{"user", "I prefer Hindi"}}, Facts: []Fact{{"user", "language", "Hindi", "I prefer Hindi", 0, 1}}, Source: "test"}
	rs, err := s.ingest(context.Background(), in, "first")
	if err != nil || len(rs) != 3 {
		t.Fatal(rs, err)
	}
	again, err := s.ingest(context.Background(), in, "first")
	if err != nil || len(again) != 3 || again[0].ID != rs[0].ID {
		t.Fatal("retry not stable", err)
	}
	changed := in
	changed.Facts = []Fact{{"user", "language", "English", "I prefer Hindi", 0, 1}}
	if _, err = s.ingest(context.Background(), changed, "first"); !errors.Is(err, ErrConflict) {
		t.Fatal("changed payload reused key", err)
	}
	in.Messages[0].Content = "I prefer English"
	in.Facts = []Fact{{"user", "language", "English", "I prefer English", 0, 1}}
	if _, err = s.ingest(context.Background(), in, "second"); err != nil {
		t.Fatal(err)
	}
	hits := e.Search(Query{User: "u", Workspace: "w", Kind: "fact"})
	if len(hits) != 1 || !strings.Contains(hits[0].Record.Content, "English") {
		t.Fatal(hits)
	}
	in.Facts[0].Quote = "invented evidence"
	before := e.Sequence()
	if _, err = s.ingest(context.Background(), in, ""); err == nil || e.Sequence() != before {
		t.Fatal("invalid evidence written")
	}
}
func TestExtractionProviderValidation(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"facts":[{"subject":"user","predicate":"language","value":"Hindi","quote":"I prefer Hindi","message_index":0,"confidence":0.95}]}`}}}})
	}))
	defer provider.Close()
	t.Setenv("MEMORYDB_LLM_URL", provider.URL)
	t.Setenv("MEMORYDB_LLM_MODEL", "test-model")
	e, _ := openTest(t)
	s := &Server{engine: e, user: "u", workspace: "w"}
	if _, err := s.ingest(context.Background(), Ingest{Messages: []Message{{"user", "I prefer Hindi"}}, Extract: true}, ""); err != nil {
		t.Fatal(err)
	}
}
func TestGoGraphAndRevisionReconstruction(t *testing.T) {
	e, _ := openTest(t)
	s := &Server{engine: e, user: "u", workspace: "w"}
	rs, err := s.indexCode(CodeInput{Repository: "repo", Branch: "main", Commit: "abc", Files: map[string]string{"a.go": "package p\nfunc A(){ B() }\nfunc B(){}"}}, "code1")
	if err != nil || len(rs) != 2 {
		t.Fatal(rs, err)
	}
	var target string
	for _, r := range rs {
		if r.Metadata["symbol"] == "B" {
			target = r.ID
		}
	}
	impact, err := e.Graph("u", "w", GraphQuery{ID: target, Reverse: true, Depth: 2})
	if err != nil || len(impact) != 2 {
		t.Fatal(impact, err)
	}
	if _, err = e.Graph("other", "w", GraphQuery{ID: target}); err != ErrMissing {
		t.Fatal("graph leak")
	}
	root, err := s.revision(RevisionInput{Repository: "repo", Branch: "main", Commit: "1", Path: "a.go", After: "hello नमस्ते"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.revision(RevisionInput{Repository: "repo", Branch: "feature", Commit: "2", Parent: root.ID, Path: "a.go", Before: "wrong", After: "new"}, "")
	if err == nil {
		t.Fatal("wrong base accepted")
	}
	r, err := s.revision(RevisionInput{Repository: "repo", Branch: "feature", Commit: "2", Parent: root.ID, Path: "a.go", Before: "hello नमस्ते", After: "hello दुनिया"}, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r.Data["patch"])
	var p Patch
	json.Unmarshal(b, &p)
	out, err := applyDiff("hello नमस्ते", p)
	if err != nil || out != "hello दुनिया" {
		t.Fatal(out, err)
	}
}
func TestDurableJobLeaseAndRestart(t *testing.T) {
	e, p := openTest(t)
	s := &Server{engine: e, user: "u", workspace: "w"}
	r, err := e.Put(Record{Kind: "job", User: "u", Workspace: "w", Content: "ingest", Data: map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Hi"}}}, Metadata: map[string]string{"status": "running", "attempts": "1", "lease_until": time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	e, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	s.engine = e
	r, err = s.runJob(context.Background(), r.ID)
	if err != nil || r.Metadata["status"] != "done" {
		t.Fatal(r, err)
	}
	seq := e.Sequence()
	if _, err = s.runJob(context.Background(), r.ID); err != nil || e.Sequence() != seq {
		t.Fatal("completed job repeated", err)
	}
}
func TestSkillApprovalCannotBeForged(t *testing.T) {
	e, _ := openTest(t)
	s := &Server{engine: e, principals: []Principal{principal("writer-token-value", "u", "w", "writer"), principal("admin-token-value", "u", "w", "admin")}}
	w := call(s, "POST", "/api/v1/memories/", "writer-token-value", map[string]any{"kind": "skill", "content": "Steps", "metadata": map[string]string{"status": "approved", "approved_by": "fake"}})
	var r Record
	json.Unmarshal(w.Body.Bytes(), &r)
	if w.Code != 201 || r.Metadata["status"] != "draft" || r.Metadata["approved_by"] != "" {
		t.Fatal(w.Body.String())
	}
	if call(s, "POST", "/api/v1/skills/"+r.ID+"/approve/", "writer-token-value", map[string]int{"version": 1}).Code != 403 {
		t.Fatal("writer approved own skill")
	}
	w = call(s, "POST", "/api/v1/skills/"+r.ID+"/approve/", "admin-token-value", map[string]int{"version": 1})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &r)
	if r.Metadata["status"] != "approved" {
		t.Fatal(r)
	}
	r.Content = "changed steps"
	w = call(s, "PUT", "/api/v1/memories/"+r.ID+"/", "writer-token-value", r)
	json.Unmarshal(w.Body.Bytes(), &r)
	if r.Metadata["status"] != "draft" {
		t.Fatal("changed skill kept approval")
	}
}

func TestCodeShadowedFunctionIsNotFalseEdge(t *testing.T) {
	e, _ := openTest(t)
	s := &Server{engine: e, user: "u", workspace: "w", role: "admin"}
	records, err := s.indexCode(CodeInput{Repository: "test", Commit: "1", Files: map[string]string{"main.go": "package p\nfunc target(){}\nfunc caller(target func()){target()}\nfunc direct(){target()}"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Metadata["symbol"] == "caller" && len(r.Links) != 0 {
			t.Fatal("shadowed call falsely linked")
		}
		if r.Metadata["symbol"] == "direct" && len(r.Links) != 1 {
			t.Fatal("direct call not resolved")
		}
	}
}
func TestCollectionCannotExpire(t *testing.T) {
	e, _ := openTest(t)
	c := collection("users")
	future := time.Now().Add(time.Hour)
	c.Expires = &future
	if _, err := e.Put(c, 0); err == nil {
		t.Fatal("expiring schema accepted")
	}
}
func TestPaginationRejectsExpiryAndMixedSort(t *testing.T) {
	e, _ := setupDocuments(t)
	r := document("a", 1)
	until := time.Now().Add(100 * time.Millisecond)
	r.Expires = &until
	if _, err := e.Put(r, 0); err != nil {
		t.Fatal(err)
	}
	e.Put(document("b", 2), 0)
	q := DocumentQuery{OrderBy: Order{{Field: "age"}}, Limit: 1}
	p, err := e.Documents("u", "w", "users", q)
	if err != nil || p.Next == "" {
		t.Fatal(p, err)
	}
	time.Sleep(time.Until(until) + time.Millisecond)
	q.Cursor = p.Next
	if _, err = e.Documents("u", "w", "users", q); !errors.Is(err, ErrConflict) {
		t.Fatal("expiry failed to invalidate cursor", err)
	}
	values := []any{nil, false, true, json.Number("2"), json.Number("10"), "a", "b", []any{1}, map[string]any{"a": 1}}
	for i := range values {
		for j := range values {
			a, b := orderValue(values[i], values[j]), orderValue(values[j], values[i])
			if (a < 0) != (b > 0) || (a == 0) != (b == 0) {
				t.Fatal("non-symmetric ordering")
			}
			for k := range values {
				if a < 0 && orderValue(values[j], values[k]) < 0 && orderValue(values[i], values[k]) >= 0 {
					t.Fatal("nontransitive ordering")
				}
			}
		}
	}
}
func TestEmptyDocumentAndMalformedJob(t *testing.T) {
	e, _ := openTest(t)
	c := collection("empty")
	c.Data = map[string]any{"fields": map[string]any{}}
	if _, err := e.Put(c, 0); err != nil {
		t.Fatal(err)
	}
	d := Record{Kind: "document", Collection: "empty", User: "u", Workspace: "w", Data: map[string]any{}}
	r, err := e.Put(d, 0)
	if err != nil || r.Data == nil {
		t.Fatal("empty object lost", err)
	}
	job, err := e.Put(Record{Kind: "job", Content: "invalid", User: "u", Workspace: "w"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{engine: e, user: "u", workspace: "w", role: "admin"}
	if _, err = s.runJob(context.Background(), job.ID); err == nil {
		t.Fatal("invalid ingest job succeeded")
	}
}
func TestAlterCollectionBackfillsExistingRows(t *testing.T) {
	e, _ := openTest(t)
	made, err := e.Put(Record{Kind: "collection", Collection: "notes", User: "u", Workspace: "w", Content: "notes",
		Visibility: "team", Data: map[string]any{"fields": map[string]any{
			"body": map[string]any{"type": "string", "required": true}, "views": map[string]any{"type": "integer"}}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.Put(Record{Kind: "document", Collection: "notes", User: "u", Workspace: "w",
		Data: map[string]any{"body": "written before the change"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	widened := clone(made)
	widened.Version = made.Version
	widened.Data = map[string]any{"fields": map[string]any{
		"body":   map[string]any{"type": "string", "required": true},
		"views":  map[string]any{"type": "integer"},
		"status": map[string]any{"type": "string"},
		"stars":  map[string]any{"type": "integer"}}}
	if _, err = e.Put(widened, made.Version); err != nil {
		t.Fatal(err)
	}
	// The row predating the field reads it at its zero value, which is what ALTER TABLE
	// ADD COLUMN does with a default, and its revision moves like any other write.
	after, err := e.Latest(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Data["status"] != "" || after.Data["stars"] != json.Number("0") || after.Version != first.Version+1 {
		t.Fatal("added fields missing from the stored row:", after.Data, after.Version)
	}
	// A required field cannot be conjured onto rows that are already there.
	refused := clone(made)
	refused.Version = after.Version
	refused.Data = map[string]any{"fields": map[string]any{
		"body":   map[string]any{"type": "string", "required": true},
		"author": map[string]any{"type": "string", "required": true}}}
	if _, err = e.Put(refused, after.Version); err == nil {
		t.Fatal("required field added over existing rows was accepted")
	}
	// A nested path would need an object nobody declared.
	nested := clone(made)
	nested.Version = after.Version
	nested.Data = map[string]any{"fields": map[string]any{
		"body":     map[string]any{"type": "string", "required": true},
		"a.b":      map[string]any{"type": "string"},
		"author_x": map[string]any{"type": "string"}}}
	if _, err = e.Put(nested, after.Version); err == nil {
		t.Fatal("nested added field was accepted")
	}
}

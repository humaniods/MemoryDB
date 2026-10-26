package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	q = DocumentQuery{OrderBy: "age", Limit: 1}
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
	q := DocumentQuery{OrderBy: "age", Limit: 1}
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

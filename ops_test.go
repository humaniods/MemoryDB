package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func opsServer(t *testing.T, ops *opsState) (*Server, string) {
	t.Helper()
	e, p := openTest(t)
	if ops != nil && ops.metrics == nil {
		ops.metrics = newMetrics()
	}
	return &Server{engine: e, ops: ops, principals: []Principal{
		principal("admin-token-0001", "ops", "w", "admin"),
		principal("writer-token-0001", "alice", "w", "writer"),
		principal("reader-token-0001", "rita", "w", "reader"),
		principal("other-admin-0001", "oscar", "other", "admin"),
	}}, p
}

func createMemory(t *testing.T, s *Server, token, content, key string) Record {
	t.Helper()
	var b bytes.Buffer
	json.NewEncoder(&b).Encode(map[string]any{"kind": "fact", "content": content})
	r := httptest.NewRequest("POST", "/api/v1/memories/", &b)
	r.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var rec Record
	json.Unmarshal(w.Body.Bytes(), &rec)
	return rec
}

func TestOnlineBackupWhileServing(t *testing.T) {
	dir := t.TempDir()
	s, _ := opsServer(t, &opsState{backupDir: dir, started: time.Now()})
	before := createMemory(t, s, "writer-token-0001", "in the backup", "")
	if w := call(s, "POST", "/api/v1/admin/backup/", "writer-token-0001", nil); w.Code != 403 {
		t.Fatal("writer took a server-wide backup:", w.Code)
	}
	w := call(s, "POST", "/api/v1/admin/backup/", "admin-token-0001", nil)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var res BackupResult
	json.Unmarshal(w.Body.Bytes(), &res)
	if filepath.Dir(res.Path) != dir || res.Sequence != s.engine.Sequence() || len(res.SHA256) != 64 || res.Bytes == 0 {
		t.Fatal("unexpected backup result:", res)
	}
	// The server keeps accepting writes after the backup, and the backup restores to the
	// state it was taken at.
	after := createMemory(t, s, "writer-token-0001", "after the backup", "")
	restored := filepath.Join(t.TempDir(), "restored.wal")
	if err := Restore(res.Path, restored); err != nil {
		t.Fatal(err)
	}
	r, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = r.Latest(before.ID); err != nil {
		t.Fatal("backup lost a committed record:", err)
	}
	if _, err = r.Latest(after.ID); err != ErrMissing {
		t.Fatal("backup contains a record written after it:", err)
	}
	// Two backups in a row get distinct names rather than refusing to overwrite.
	time.Sleep(2 * time.Millisecond)
	if w = call(s, "POST", "/api/v1/admin/backup/", "admin-token-0001", nil); w.Code != 201 {
		t.Fatal("second backup:", w.Code, w.Body.String())
	}
	off, _ := opsServer(t, &opsState{started: time.Now()})
	if w = call(off, "POST", "/api/v1/admin/backup/", "admin-token-0001", nil); w.Code != 404 || !strings.Contains(w.Body.String(), "-backup-dir") {
		t.Fatal("backup without -backup-dir:", w.Code, w.Body.String())
	}
}

func TestMetricsEndpoint(t *testing.T) {
	s, _ := opsServer(t, &opsState{started: time.Now()})
	createMemory(t, s, "writer-token-0001", "counted", "")
	createMemory(t, s, "writer-token-0001", "counted too", "")
	call(s, "POST", "/api/v1/search/", "reader-token-0001", map[string]any{"text": "counted"})
	if w := call(s, "GET", "/metrics", "reader-token-0001", nil); w.Code != 403 {
		t.Fatal("reader read server-wide metrics:", w.Code)
	}
	if w := call(s, "GET", "/metrics", "wrong-token", nil); w.Code != 401 {
		t.Fatal("unauthenticated metrics:", w.Code)
	}
	w := call(s, "GET", "/metrics", "admin-token-0001", nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatal(w.Code, w.Header())
	}
	for _, want := range []string{
		`memorydb_build_info{version="` + version + `"} 1`,
		"memorydb_journal_sequence 2",
		`memorydb_records{kind="fact"} 2`,
		"memorydb_storage_poisoned 0",
		`memorydb_http_requests_total{route="memories",code="201"} 2`,
		`memorydb_http_requests_total{route="search",code="200"} 1`,
		`memorydb_http_requests_total{route="metrics",code="403"} 1`,
		`memorydb_http_request_duration_seconds_bucket{route="memories",le="+Inf"} 2`,
		`memorydb_http_request_duration_seconds_count{route="search"} 1`,
	} {
		if !strings.Contains(body, want+"\n") {
			t.Fatalf("metrics missing %q in:\n%s", want, body)
		}
	}
	// Ids never become label values.
	if strings.Contains(body, "/api/v1/") {
		t.Fatal("raw path leaked into labels")
	}
}

func TestLimiterRefillsAtRate(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(2, 3)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("a"); !ok {
			t.Fatal("burst refused at", i)
		}
	}
	ok, wait := l.allow("a")
	if ok || wait != 500*time.Millisecond {
		t.Fatal("empty bucket:", ok, wait)
	}
	if ok, _ = l.allow("b"); !ok {
		t.Fatal("one principal drained another's bucket")
	}
	now = now.Add(500 * time.Millisecond)
	if ok, _ = l.allow("a"); !ok {
		t.Fatal("bucket did not refill at rate")
	}
	now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		if ok, _ = l.allow("a"); !ok {
			t.Fatal("refill exceeded or fell short of burst at", i)
		}
	}
	if ok, _ = l.allow("a"); ok {
		t.Fatal("refill exceeded burst")
	}
}

func TestRateLimitOverHTTP(t *testing.T) {
	s, _ := opsServer(t, &opsState{limiter: newLimiter(0.001, 2), started: time.Now()})
	for i := 0; i < 2; i++ {
		if w := call(s, "GET", "/api/v1/health/", "writer-token-0001", nil); w.Code != 200 {
			t.Fatal(i, w.Code)
		}
	}
	w := call(s, "GET", "/api/v1/health/", "writer-token-0001", nil)
	if w.Code != 429 {
		t.Fatal("limit not enforced:", w.Code)
	}
	if retry, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || retry < 1 {
		t.Fatal("Retry-After:", w.Header().Get("Retry-After"))
	}
	if w = call(s, "GET", "/api/v1/health/", "reader-token-0001", nil); w.Code != 200 {
		t.Fatal("another principal was limited:", w.Code)
	}
	// Scrapes are never limited, so monitoring stays up while a client is being throttled.
	for i := 0; i < 3; i++ {
		if w = call(s, "GET", "/metrics", "admin-token-0001", nil); w.Code != 200 {
			t.Fatal("metrics scrape limited:", w.Code)
		}
	}
	if !strings.Contains(w.Body.String(), "memorydb_rate_limited_total 1\n") {
		t.Fatal("rate-limited request not counted")
	}
}

func TestPurgeErasesDeletedRecordFromJournal(t *testing.T) {
	s, p := opsServer(t, &opsState{started: time.Now()})
	secret := "aadhaar 1234-5678-9012 belongs to priya"
	gone := createMemory(t, s, "writer-token-0001", secret, "create-secret")
	kept := createMemory(t, s, "writer-token-0001", "unrelated fact stays", "")
	linker := Record{Kind: "fact", Content: "links to the purged record", User: "alice", Workspace: "w", Source: "test", Links: []string{gone.ID}}
	linkRec, err := s.engine.Put(linker, 0)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(s, "POST", "/api/v1/admin/purge/", "admin-token-0001", map[string]any{"ids": []string{gone.ID}}); w.Code != 400 || !strings.Contains(w.Body.String(), "must be deleted") {
		t.Fatal("live record purged:", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("DELETE", "/api/v1/memories/"+gone.ID+"/", nil)
	r.Header.Set("Authorization", "Bearer writer-token-0001")
	r.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("delete:", w.Code, w.Body.String())
	}
	raw, _ := os.ReadFile(p)
	if !bytes.Contains(raw, []byte(secret)) {
		t.Fatal("test premise: a deleted record's content is still in the journal")
	}
	if w = call(s, "POST", "/api/v1/admin/purge/", "writer-token-0001", map[string]any{"ids": []string{gone.ID}}); w.Code != 403 {
		t.Fatal("writer purged:", w.Code)
	}
	if w = call(s, "POST", "/api/v1/admin/purge/", "other-admin-0001", map[string]any{"ids": []string{gone.ID}}); w.Code != 404 {
		t.Fatal("admin of another workspace purged:", w.Code)
	}
	if w = call(s, "POST", "/api/v1/admin/purge/", "admin-token-0001", map[string]any{"ids": []string{gone.ID}}); w.Code != 200 {
		t.Fatal("purge:", w.Code, w.Body.String())
	}
	raw, _ = os.ReadFile(p)
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("purged content is still in the journal")
	}
	if !bytes.Contains(raw, []byte("unrelated fact stays")) {
		t.Fatal("purge removed an unrelated record")
	}
	// Reopen from disk: the record and every revision of it are gone, others are intact.
	s.engine.Close()
	e, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	s.engine = e
	if h := e.History(gone.ID); len(h) != 0 {
		t.Fatal("purged history survived reopen:", h)
	}
	if _, err = e.Latest(kept.ID); err != nil {
		t.Fatal(err)
	}
	// Replaying the original create with its idempotency key must not bring it back.
	var b bytes.Buffer
	json.NewEncoder(&b).Encode(map[string]any{"kind": "fact", "content": secret})
	r = httptest.NewRequest("POST", "/api/v1/memories/", &b)
	r.Header.Set("Authorization", "Bearer writer-token-0001")
	r.Header.Set("Idempotency-Key", "create-secret")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("idempotent retry of a purged create:", w.Code, w.Body.String())
	}
	if hits := e.Search(Query{User: "alice", Workspace: "w", Text: "aadhaar"}); len(hits) != 0 {
		t.Fatal("purged record is searchable")
	}
	// A record that linked to the purged one still reads and traverses.
	if w = call(s, "POST", "/api/v1/graph/", "writer-token-0001", map[string]any{"id": linkRec.ID}); w.Code != 200 {
		t.Fatal("graph over a dangling link:", w.Code, w.Body.String())
	}
	if w = call(s, "POST", "/api/v1/admin/purge/", "admin-token-0001", map[string]any{"ids": []string{gone.ID}}); w.Code != 404 {
		t.Fatal("second purge of the same id:", w.Code)
	}
}

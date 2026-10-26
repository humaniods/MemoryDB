package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func sample(text string) Record {
	return Record{Kind: "fact", Content: text, User: "u", Workspace: "w", Source: "test"}
}
func openTest(t *testing.T) (*Engine, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.wal")
	e, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e, p
}
func TestDurabilityHistoryAndConflict(t *testing.T) {
	e, p := openTest(t)
	r, err := e.Put(sample("prefers Hindi"), 0)
	if err != nil {
		t.Fatal(err)
	}
	r.Content = "prefers English"
	v2, err := e.Put(r, 1)
	if err != nil || v2.Version != 2 {
		t.Fatal(v2, err)
	}
	if _, err = e.Put(r, 1); err != ErrConflict {
		t.Fatal(err)
	}
	e.Close()
	recovered, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	h := recovered.History(r.ID)
	if len(h) != 2 || h[0].Content != "prefers Hindi" || h[1].Content != "prefers English" {
		t.Fatal(h)
	}
}
func TestTruncatedTailAndCorruption(t *testing.T) {
	e, p := openTest(t)
	_, err := e.Put(sample("keep me"), 0)
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	before, _ := os.Stat(p)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	f.Write([]byte{0, 0, 0, 100, 1, 2})
	f.Close()
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Search(Query{User: "u", Workspace: "w"})) != 1 {
		t.Fatal("lost committed record")
	}
	r.Close()
	after, _ := os.Stat(p)
	if before.Size() != after.Size() {
		t.Fatal("tail not repaired")
	}
	b, _ := os.ReadFile(p)
	b[len(b)-1] ^= 1
	os.WriteFile(p, b, 0600)
	if x, err := Open(p); err == nil {
		x.Close()
		t.Fatal("corruption accepted")
	}
}
func TestIsolationSearchVectorsExpiryLinks(t *testing.T) {
	e, _ := openTest(t)
	a := sample("Hindi voice preference")
	a.Vector = []float64{1, 0}
	r, err := e.Put(a, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := sample("private Hindi data")
	b.User = "another"
	b.Vector = []float64{1, 0}
	foreign, _ := e.Put(b, 0)
	c := sample("expired Hindi")
	past := time.Now().Add(-time.Hour)
	c.Expires = &past
	e.Put(c, 0)
	hits := e.Search(Query{User: "u", Workspace: "w", Text: "Hindi", Vector: []float64{1, 0}})
	if len(hits) != 1 || hits[0].Record.ID != r.ID {
		t.Fatal(hits)
	}
	d := sample("invalid link")
	d.Links = []string{foreign.ID}
	if _, err = e.Put(d, 0); err == nil {
		t.Fatal("cross-scope link accepted")
	}
	if len(e.Search(Query{User: "u", Workspace: "w", Text: "absent"})) != 0 {
		t.Fatal("unrelated match")
	}
	r.Deleted = true
	if _, err = e.Put(r, 1); err != nil {
		t.Fatal(err)
	}
	if len(e.Search(Query{User: "u", Workspace: "w"})) != 0 {
		t.Fatal("deleted item visible")
	}
}
func TestExclusiveOwnershipAndConcurrentWrites(t *testing.T) {
	e, p := openTest(t)
	if other, err := Open(p); err == nil {
		other.Close()
		t.Fatal("second owner accepted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := e.Put(sample(fmt.Sprint("entry ", i)), 0); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(e.Search(Query{User: "u", Workspace: "w"})) != 30 {
		t.Fatal("lost concurrent writes")
	}
}
func TestHTTPFlow(t *testing.T) {
	e, _ := openTest(t)
	s := &Server{engine: e, token: "test-token-0123456789", user: "local-user", workspace: "default", role: "admin"}
	req := func(method, path string, body any, token string, version string) *httptest.ResponseRecorder {
		var b bytes.Buffer
		if body != nil {
			json.NewEncoder(&b).Encode(body)
		}
		r := httptest.NewRequest(method, path, &b)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if req("GET", "/api/v1/memories/", nil, "", "").Code != 401 {
		t.Fatal("authentication bypass")
	}
	token := s.token
	w := req("POST", "/api/v1/memories/", map[string]any{"kind": "fact", "content": "Hindi voice", "user": "spoof", "workspace": "other"}, token, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var r Record
	json.Unmarshal(w.Body.Bytes(), &r)
	if r.User != "local-user" || r.Workspace != "default" {
		t.Fatal("caller-controlled identity")
	}
	r.Content = "English voice"
	w = req("PUT", "/api/v1/memories/"+r.ID+"/", r, token, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if req("PUT", "/api/v1/memories/"+r.ID+"/", r, token, "").Code != 409 {
		t.Fatal("stale update accepted")
	}
	if req("DELETE", "/api/v1/memories/"+r.ID+"/", nil, token, "").Code != 400 {
		t.Fatal("unguarded delete")
	}
	if req("DELETE", "/api/v1/memories/"+r.ID+"/", nil, token, "2").Code != http.StatusOK {
		t.Fatal("delete failed")
	}
	if req("GET", "/api/v1/memories/"+r.ID+"/", nil, token, "").Code != 404 {
		t.Fatal("deleted item returned")
	}
	w = req("GET", "/api/v1/memories/"+r.ID+"/history/", nil, token, "")
	var h struct {
		Results []Record `json:"results"`
	}
	json.Unmarshal(w.Body.Bytes(), &h)
	if len(h.Results) != 3 {
		t.Fatal(w.Body.String())
	}
	if req("POST", "/api/v1/memories/", map[string]any{"unknown": "field"}, token, "").Code != 400 {
		t.Fatal("unknown field accepted")
	}
}

func TestCosineLargeFiniteVectors(t *testing.T) {
	if c := cosine([]float64{1e308, 1e308}, []float64{1e308, 1e308}); c < 0.999 || c > 1.001 {
		t.Fatal(c)
	}
	if cosine([]float64{0}, []float64{1}) != 0 {
		t.Fatal("zero vector")
	}
}

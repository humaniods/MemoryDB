package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func syncServer(t *testing.T) (*Server, string) {
	t.Helper()
	e, _ := openTest(t)
	s := &Server{engine: e, principals: []Principal{
		principal("sync-admin-token", "u", "w", "admin"),
		principal("sync-writer-token", "u", "w", "writer"),
		principal("sync-reader-token", "u", "w", "reader"),
	}}
	w := call(s, "POST", "/api/v1/collections/", "sync-admin-token", map[string]any{
		"name": "customers",
		"schema": map[string]any{
			"fields": map[string]any{"id": map[string]any{"type": "string", "required": true},
				"name": map[string]any{"type": "string"}, "region": map[string]any{"type": "string"},
				"mrr": map[string]any{"type": "number"}},
			"unique":  []string{"id"},
			"indexes": []string{"region"},
		}})
	if w.Code != 201 {
		t.Fatalf("create collection: %d %s", w.Code, w.Body.String())
	}
	return s, "sync-writer-token"
}

func syncBody(items ...map[string]any) map[string]any {
	rows := []map[string]any{}
	for _, i := range items {
		rows = append(rows, map[string]any{"data": i})
	}
	return map[string]any{"collection": "customers", "key_field": "id", "items": rows}
}

func syncCounts(t *testing.T, w *httptest.ResponseRecorder) map[string]int {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body.String())
	}
	counts := map[string]int{}
	for _, k := range []string{"created", "updated", "unchanged", "removed", "written"} {
		if v, ok := out[k]; ok {
			n, _ := v.(float64)
			counts[k] = int(n)
		}
	}
	return counts
}

func TestConnectorSyncWritesOnlyChangedRows(t *testing.T) {
	s, token := syncServer(t)

	w := call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(
		map[string]any{"id": "c-1", "name": "Rani Textiles", "region": "Tamil Nadu", "mrr": 48000},
		map[string]any{"id": "c-2", "name": "Deccan Logistics", "region": "Telangana", "mrr": 91000},
		map[string]any{"id": "c-3", "name": "Kaaveri Dairy", "region": "Karnataka", "mrr": 4200}))
	if w.Code != 200 {
		t.Fatalf("first sync: %d %s", w.Code, w.Body.String())
	}
	got := syncCounts(t, w)
	if got["created"] != 3 || got["written"] != 3 {
		t.Fatalf("first sync = %+v", got)
	}
	before := s.engine.Sequence()

	// Identical re-sync must not touch the journal at all; that is the whole point.
	w = call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(
		map[string]any{"id": "c-1", "name": "Rani Textiles", "region": "Tamil Nadu", "mrr": 48000},
		map[string]any{"id": "c-2", "name": "Deccan Logistics", "region": "Telangana", "mrr": 91000},
		map[string]any{"id": "c-3", "name": "Kaaveri Dairy", "region": "Karnataka", "mrr": 4200}))
	got = syncCounts(t, w)
	if got["created"] != 0 || got["updated"] != 0 || got["removed"] != 0 || got["unchanged"] != 3 {
		t.Fatalf("identical resync = %+v", got)
	}
	if got["written"] != 0 {
		t.Fatalf("identical resync wrote %d records", got["written"])
	}
	if s.engine.Sequence() != before {
		t.Fatalf("no-op sync advanced the journal from %d to %d", before, s.engine.Sequence())
	}

	w = call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(
		map[string]any{"id": "c-1", "name": "Rani Textiles", "region": "Tamil Nadu", "mrr": 52000},
		map[string]any{"id": "c-2", "name": "Deccan Logistics", "region": "Telangana", "mrr": 91000},
		map[string]any{"id": "c-3", "name": "Kaaveri Dairy", "region": "Karnataka", "mrr": 4200}))
	got = syncCounts(t, w)
	if got["updated"] != 1 || got["unchanged"] != 2 || s.engine.Sequence() != before+1 {
		t.Fatalf("one changed row = %+v sequence %d", got, s.engine.Sequence())
	}

	history := call(s, "GET", "/api/v1/documents/customers/", token, nil)
	var page DocumentPage
	if err := json.Unmarshal(history.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Results {
		if r.Metadata["connector"] != "stripe" || r.Metadata["source_hash"] == "" {
			t.Fatalf("mirrored row lost its connector provenance: %+v", r.Metadata)
		}
		if !strings.Contains(r.Content, "Rani Textiles") && r.Metadata["connector_key"] == "c-1" {
			t.Fatalf("mirrored row has no searchable content: %q", r.Content)
		}
		// The widened row keeps its earlier revisions, so a sync is auditable.
		if r.Version != 2 && r.Metadata["connector_key"] == "c-1" {
			t.Fatalf("changed row version = %d", r.Version)
		}
	}
	// Mirrored rows are documents, so they are searched with kind=document; the memory
	// search excludes documents by design.
	if w := call(s, "POST", "/api/v1/search/", token, map[string]any{"text": "Rani", "kind": "document"}); !strings.Contains(w.Body.String(), "Rani Textiles") {
		t.Fatalf("mirrored rows are not searchable: %s", w.Body.String())
	}

	// A row the source dropped stays until delete_missing asks for it.
	w = call(s, "POST", "/api/v1/sources/stripe/sync/", token, map[string]any{
		"collection": "customers", "key_field": "id", "delete_missing": true,
		"items": []map[string]any{{"data": map[string]any{"id": "c-1", "name": "Rani Textiles", "region": "Tamil Nadu", "mrr": 52000}}}})
	got = syncCounts(t, w)
	if got["removed"] != 2 || got["unchanged"] != 1 {
		t.Fatalf("delete_missing = %+v", got)
	}

	list := call(s, "GET", "/api/v1/sources/", token, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "stripe") {
		t.Fatalf("source listing = %s", list.Body.String())
	}
}

// mirroredRow reads one synced row back through the query endpoint.
func mirroredRow(t *testing.T, s *Server, token, key string) Record {
	t.Helper()
	w := call(s, "POST", "/api/v1/query/customers/", token, map[string]any{
		"where": []map[string]any{{"field": "id", "op": "eq", "value": key}}})
	var page DocumentPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode query: %v (%s)", err, w.Body.String())
	}
	if len(page.Results) != 1 {
		t.Fatalf("query for %s = %s", key, w.Body.String())
	}
	return page.Results[0]
}

func TestConnectorComparesAgainstTheLastPayloadSynced(t *testing.T) {
	s, token := syncServer(t)
	row := map[string]any{"id": "c-1", "name": "Rani Textiles", "region": "Tamil Nadu", "mrr": 48000}
	if w := call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(row)); w.Code != 200 {
		t.Fatalf("seed sync: %d %s", w.Code, w.Body.String())
	}
	// A person edits the mirrored row in the console.
	live := mirroredRow(t, s, token, "c-1")
	if w := call(s, "PATCH", "/api/v1/documents/customers/"+live.ID, token,
		map[string]any{"version": live.Version, "set": map[string]any{"mrr": 999999}}); w.Code != 200 {
		t.Fatalf("hand edit: %d %s", w.Code, w.Body.String())
	}
	// The stored hash is of the payload this connector last sent, so a source that has
	// not moved reports the edited row unchanged instead of reverting it.
	w := call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(row))
	if got := syncCounts(t, w); got["unchanged"] != 1 || got["updated"] != 0 {
		t.Fatalf("sync of unchanged source = %+v", got)
	}
	if mrr := mirroredRow(t, s, token, "c-1").Data["mrr"]; mrr != 999999.0 {
		t.Fatalf("a no-change sync reverted the hand edit: %v", mrr)
	}
	// Once the source row changes, the source wins - and the edit stays as a revision.
	w = call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(
		map[string]any{"id": "c-1", "name": "Rani Textiles", "region": "Tamil Nadu", "mrr": 52000}))
	if got := syncCounts(t, w); got["updated"] != 1 {
		t.Fatalf("sync of changed source = %+v", got)
	}
	live = mirroredRow(t, s, token, "c-1")
	if live.Data["mrr"] != 52000.0 {
		t.Fatalf("source value not applied: %v", live.Data["mrr"])
	}
	if h := call(s, "GET", "/api/v1/memories/"+live.ID+"/history/", token, nil); !strings.Contains(h.Body.String(), "999999") {
		t.Fatalf("hand edit missing from history: %s", h.Body.String())
	}
}

func TestConnectorRefusesToRewriteForeignRows(t *testing.T) {
	s, token := syncServer(t)
	w := call(s, "POST", "/api/v1/documents/customers/", token, map[string]any{
		"data": map[string]any{"id": "c-9", "name": "Hand written row"}})
	if w.Code != 201 {
		t.Fatalf("direct insert: %d %s", w.Code, w.Body.String())
	}
	w = call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(map[string]any{"id": "c-9", "name": "Synced over it"}))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "already exists") {
		t.Fatalf("foreign key accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestConnectorInputLimitsAndPermissions(t *testing.T) {
	s, token := syncServer(t)
	if w := call(s, "POST", "/api/v1/sources/stripe/sync/", token, map[string]any{
		"collection": "nope", "key_field": "id",
		"items": []map[string]any{{"data": map[string]any{"id": "a"}}}}); w.Code != 400 || !strings.Contains(w.Body.String(), "does not exist") {
		t.Fatalf("missing collection: %d %s", w.Code, w.Body.String())
	}
	if w := call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody()); w.Code != 400 {
		t.Fatalf("empty sync: %d %s", w.Code, w.Body.String())
	}
	if w := call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(map[string]any{"name": "no key"})); w.Code != 400 || !strings.Contains(w.Body.String(), "key_field") {
		t.Fatalf("missing key field: %d %s", w.Code, w.Body.String())
	}
	if w := call(s, "POST", "/api/v1/sources/stripe/sync/", token, syncBody(
		map[string]any{"id": "same"}, map[string]any{"id": "same"})); w.Code != 400 || !strings.Contains(w.Body.String(), "duplicate") {
		t.Fatalf("duplicate key accepted: %s", w.Body.String())
	}
	if w := call(s, "POST", "/api/v1/sources/stripe/sync/", "sync-reader-token", syncBody(map[string]any{"id": "c-1"})); w.Code != 403 {
		t.Fatalf("reader sync: %d %s", w.Code, w.Body.String())
	}
	if w := call(s, "POST", "/api/v1/sources/bad%20name/sync/", token, syncBody(map[string]any{"id": "c-1"})); w.Code != 400 {
		t.Fatalf("invalid connector name: %d %s", w.Code, w.Body.String())
	}
}

func TestDemoSeedIsRepeatableAndSearchable(t *testing.T) {
	e, _ := openTest(t)
	s := &Server{engine: e, principals: []Principal{principal("demo-admin-token", "u", "w", "admin")}}
	w := call(s, "POST", "/api/v1/demo/", "demo-admin-token", nil)
	if w.Code != 200 {
		t.Fatalf("demo seed: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["seeded"] != true {
		t.Fatalf("seed response = %s", w.Body.String())
	}
	rows := out["rows"].(map[string]any)
	if int(rows["customers"].(float64)) != 6 || int(rows["invoices"].(float64)) != 8 {
		t.Fatalf("seeded rows = %v", rows)
	}
	if int(out["memories"].(float64)) < 4 {
		t.Fatalf("seeded memories = %v", out["memories"])
	}
	w = call(s, "POST", "/api/v1/demo/", "demo-admin-token", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"seeded":false`) {
		t.Fatalf("second seed: %d %s", w.Code, w.Body.String())
	}
	if w = call(s, "POST", "/api/v1/search/", "demo-admin-token", map[string]any{"text": "renewal"}); !strings.Contains(w.Body.String(), "unlimited-term renewal") {
		t.Fatalf("sample documents not searchable: %s", w.Body.String())
	}
	if w = call(s, "POST", "/api/v1/search/", "demo-admin-token", map[string]any{"text": "Rani", "kind": "document"}); !strings.Contains(w.Body.String(), "Rani Textiles") {
		t.Fatalf("seeded mirror rows not searchable: %s", w.Body.String())
	}
	if w = call(s, "POST", "/api/v1/search/", "demo-admin-token", map[string]any{"text": "seats", "kind": "fact"}); !strings.Contains(w.Body.String(), "85") {
		t.Fatalf("sample facts not searchable: %s", w.Body.String())
	}
	if w = call(s, "POST", "/api/v1/query/customers/", "demo-admin-token", map[string]any{
		"where": []map[string]any{{"field": "region", "op": "eq", "value": "Rajasthan"}}}); !strings.Contains(w.Body.String(), "Thar Solar") {
		t.Fatalf("sample query: %s", w.Body.String())
	}
	// Re-syncing the sample table with one changed row proves the seed is a real connector.
	w = call(s, "POST", "/api/v1/sources/acme-sample/sync/", "demo-admin-token", map[string]any{
		"collection": "customers", "key_field": "id",
		"items": []map[string]any{{"data": map[string]any{"id": "c-104", "name": "Thar Solar", "region": "Rajasthan", "plan": "scale", "mrr": 30000, "owner": "nadha"}}}})
	if got := syncCounts(t, w); got["updated"] != 1 || got["unchanged"] != 0 {
		t.Fatalf("resync of seeded table = %+v (%s)", got, w.Body.String())
	}
	if w = call(s, "POST", "/api/v1/demo/", "demo-admin-token", nil); w.Code != 200 {
		t.Fatalf("idempotent seed: %d", w.Code)
	}
}

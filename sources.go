package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A connector mirrors an external table into a collection. Each mirrored row carries the
// hash of the data it was written from, so re-running a sync writes only the rows whose
// data actually changed. Sources that can report their own changes (a database with
// change capture) are not required here, which is what makes this usable for the files,
// CSV exports and REST lists that most teams actually have.
type SyncItem struct {
	Data    map[string]any `json:"data"`
	Content string         `json:"content,omitempty"`
}
type SyncInput struct {
	Collection    string     `json:"collection"`
	KeyField      string     `json:"key_field"`
	Items         []SyncItem `json:"items"`
	DeleteMissing bool       `json:"delete_missing,omitempty"`
	Source        string     `json:"source,omitempty"`
}

const (
	syncMaxItems = 500
	syncMaxKey   = 128
	scanPageSize = 500
	scanMaxRows  = 20000
	contentMax   = 8000
)

func sourceKey(v any) (string, error) {
	switch x := v.(type) {
	case string:
		if v := strings.TrimSpace(x); v != "" {
			return v, nil
		}
	case json.Number:
		return x.String(), nil
	case bool:
		return strconv.FormatBool(x), nil
	}
	return "", errors.New("key_field must hold a non-empty string, number or boolean")
}

// renderContent gives a mirrored row searchable text without asking every source for a
// title column; the caller can always supply a better content string on the item.
func renderContent(data map[string]any) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		switch v := data[k].(type) {
		case string:
			parts = append(parts, k+": "+v)
		case json.Number:
			parts = append(parts, k+": "+v.String())
		default:
			b, _ := json.Marshal(v)
			parts = append(parts, k+": "+string(b))
		}
	}
	content := strings.Join(parts, " | ")
	if runes := []rune(content); len(runes) > contentMax {
		return string(runes[:contentMax])
	}
	return content
}

func (s *Server) collectionExists(name string) bool {
	for _, c := range s.engine.Collections(s.workspace) {
		if c.Collection == name && !c.Deleted {
			return true
		}
	}
	return false
}

// scanCollection reads the rows one connector already owns. Rows written by hand or by
// another connector are reported separately so a shared key can be refused instead of
// silently rewriting someone else's data. A row's identity is the key the sync wrote it
// under, which survives the source column being renamed later.
func (s *Server) scanCollection(connector, collection, keyField string) (map[string]Record, map[string]string, error) {
	mine, foreign := map[string]Record{}, map[string]string{}
	for offset := 0; offset < scanMaxRows; offset += scanPageSize {
		page, err := s.engine.Documents(s.user, s.workspace, collection, DocumentQuery{Limit: scanPageSize, Offset: offset})
		if err != nil {
			return nil, nil, err
		}
		for _, r := range page.Results {
			key := r.Metadata["connector_key"]
			if key == "" {
				raw, ok := valueAt(r.Data, keyField)
				if !ok {
					continue
				}
				if key, err = sourceKey(raw); err != nil {
					continue
				}
			}
			owner := r.Metadata["connector"]
			switch {
			case owner == connector:
				if _, clash := mine[key]; !clash {
					mine[key] = r
				}
			default:
				if _, clash := mine[key]; !clash {
					if owner != "" {
						foreign[key] = "connector " + owner
					} else {
						foreign[key] = "a row written directly"
					}
				}
			}
		}
		if len(page.Results) < scanPageSize {
			return mine, foreign, nil
		}
	}
	return nil, nil, errors.New("collection too large to sync (over 20000 rows)")
}

func (s *Server) syncChanges(connector string, in SyncInput) ([]Change, map[string]any, error) {
	if !validName(in.Collection) {
		return nil, nil, errors.New("invalid collection")
	}
	if !validName(in.KeyField) {
		return nil, nil, errors.New("invalid key_field")
	}
	if len(in.Items) == 0 || len(in.Items) > syncMaxItems {
		return nil, nil, errors.New("a sync carries between 1 and 500 rows")
	}
	if in.Source == "" {
		in.Source = "sync:" + connector
	}
	if !s.collectionExists(in.Collection) {
		return nil, nil, errors.New("collection " + in.Collection + " does not exist; create it first")
	}
	wanted := map[string]SyncItem{}
	order := []string{}
	for _, item := range in.Items {
		if item.Data == nil {
			return nil, nil, errors.New("each row needs a JSON object under data")
		}
		raw, ok := valueAt(item.Data, in.KeyField)
		if !ok {
			return nil, nil, errors.New("key_field " + in.KeyField + " missing from a row")
		}
		key, err := sourceKey(raw)
		if err != nil {
			return nil, nil, err
		}
		if len(key) > syncMaxKey {
			return nil, nil, errors.New("sync key too long")
		}
		if _, dup := wanted[key]; dup {
			return nil, nil, errors.New("duplicate key " + key + " in one sync")
		}
		wanted[key] = item
		order = append(order, key)
	}
	sort.Strings(order)
	mine, foreign, err := s.scanCollection(connector, in.Collection, in.KeyField)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	changes := []Change{}
	created, updated, unchanged, removed := 0, 0, 0, 0
	for _, key := range order {
		item := wanted[key]
		content := item.Content
		if strings.TrimSpace(content) == "" {
			content = renderContent(item.Data)
		}
		hash := hashText(string(mustJSON(item.Data)) + "\x00" + content)
		old, had := mine[key]
		if !had {
			if clash, isForeign := foreign[key]; isForeign {
				return nil, nil, errors.New("key " + key + " already exists in " + in.Collection + " (written by " + clash + ")")
			}
			changes = append(changes, Change{Record{
				Kind: "document", Collection: in.Collection, Data: item.Data, Content: content,
				Visibility: "team", Layer: "mirror", Source: in.Source,
				User: s.user, Workspace: s.workspace,
				Metadata: map[string]string{"connector": connector, "connector_key": key, "source_hash": hash, "synced_at": now},
			}, 0})
			created++
			continue
		}
		if old.Metadata["source_hash"] == hash {
			unchanged++
			continue
		}
		if !s.canWrite(old) {
			return nil, nil, errors.New("key " + key + " belongs to " + old.User + "; only its owner or an admin may resync it")
		}
		rec := clone(old)
		rec.Data, rec.Content = item.Data, content
		rec.Source = in.Source
		if rec.Metadata == nil {
			rec.Metadata = map[string]string{}
		}
		rec.Metadata["connector"], rec.Metadata["connector_key"] = connector, key
		rec.Metadata["source_hash"], rec.Metadata["synced_at"] = hash, now
		changes = append(changes, Change{rec, old.Version})
		updated++
	}
	if in.DeleteMissing {
		for _, r := range mine {
			key := r.Metadata["connector_key"]
			if key == "" {
				continue
			}
			if _, kept := wanted[key]; kept {
				continue
			}
			rec := clone(r)
			rec.Deleted = true
			changes = append(changes, Change{rec, r.Version})
			removed++
		}
	}
	counts := map[string]any{"connector": connector, "collection": in.Collection, "key_field": in.KeyField,
		"created": created, "updated": updated, "unchanged": unchanged, "removed": removed,
		"rows_in_source": len(order), "synced_at": now}
	return changes, counts, nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (s *Server) sourceRoutes(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path == "/api/v1/sources/" && r.Method == "GET" {
		respond(w, 200, map[string]any{"results": s.listSources()})
		return true
	}
	if strings.HasPrefix(path, "/api/v1/sources/") && strings.HasSuffix(path, "/sync/") && r.Method == "POST" {
		connector := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/sources/"), "/sync/")
		if !validName(connector) {
			fail(w, errors.New("invalid connector name"))
			return true
		}
		var in SyncInput
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return true
		}
		changes, counts, err := s.syncChanges(connector, in)
		if err != nil {
			fail(w, err)
			return true
		}
		if len(changes) > 0 {
			records, err := s.engine.Batch(changes, s.key(r))
			if err != nil {
				fail(w, err)
				return true
			}
			counts["written"] = len(records)
		}
		counts["sequence"] = s.engine.Sequence()
		respond(w, 200, counts)
		return true
	}
	return false
}

// listSources reports each connector from the rows it owns, so the state a sync reads
// lives with the data and survives a restart without a separate registry.
func (s *Server) listSources() []map[string]any {
	type acc struct {
		rows        int
		latest      string
		collections map[string]bool
	}
	found := map[string]*acc{}
	for _, c := range s.engine.Collections(s.workspace) {
		if c.Deleted {
			continue
		}
		for offset := 0; offset < scanMaxRows; offset += scanPageSize {
			page, err := s.engine.Documents(s.user, s.workspace, c.Collection, DocumentQuery{Limit: scanPageSize, Offset: offset})
			if err != nil {
				break
			}
			for _, r := range page.Results {
				name := r.Metadata["connector"]
				if name == "" {
					continue
				}
				a := found[name]
				if a == nil {
					a = &acc{collections: map[string]bool{}}
					found[name] = a
				}
				a.rows++
				a.collections[c.Collection] = true
				if r.Metadata["synced_at"] > a.latest {
					a.latest = r.Metadata["synced_at"]
				}
			}
			if len(page.Results) < scanPageSize {
				break
			}
		}
	}
	out := []map[string]any{}
	for name, a := range found {
		cols := []string{}
		for c := range a.collections {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		out = append(out, map[string]any{"connector": name, "collections": cols, "rows": a.rows, "last_synced": a.latest})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["connector"].(string) < out[j]["connector"].(string) })
	return out
}

// A reader principal may POST searches but not writes; syncs and seeds are writes, and the
// generic gate in ServeHTTP already refuses them, so only the admin-only demo needs a check.
func (s *Server) demoRoute(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v1/demo/" || r.Method != "POST" {
		return false
	}
	if s.role != "admin" {
		failure(w, 403, errors.New("admin token required to seed the sample"))
		return true
	}
	seed, err := s.seedDemo(r)
	if err != nil {
		fail(w, err)
		return true
	}
	respond(w, 200, seed)
	return true
}

func row(key string, data map[string]any) SyncItem {
	data["id"] = key
	return SyncItem{Data: data}
}

func (s *Server) seedDemo(r *http.Request) (map[string]any, error) {
	if s.collectionExists("customers") {
		return map[string]any{"seeded": false, "reason": "the sample customers collection already exists",
			"sources": s.listSources()}, nil
	}
	schemas := []Change{
		{Record{Kind: "collection", Collection: "customers", Content: "customers", Visibility: "team", Data: map[string]any{
			"fields": map[string]any{"id": map[string]any{"type": "string", "required": true}, "name": map[string]any{"type": "string", "required": true},
				"region": map[string]any{"type": "string"}, "plan": map[string]any{"type": "string"}, "mrr": map[string]any{"type": "number"}, "owner": map[string]any{"type": "string"}},
			"unique": []string{"id"}, "indexes": []string{"region", "plan"}}}, 0},
		{Record{Kind: "collection", Collection: "subscriptions", Content: "subscriptions", Visibility: "team", Data: map[string]any{
			"fields": map[string]any{"id": map[string]any{"type": "string", "required": true}, "customer_id": map[string]any{"type": "string"},
				"plan": map[string]any{"type": "string"}, "seats": map[string]any{"type": "integer"}, "status": map[string]any{"type": "string"}, "renews_at": map[string]any{"type": "string"}},
			"unique": []string{"id"}, "indexes": []string{"customer_id", "status"}}}, 0},
		{Record{Kind: "collection", Collection: "invoices", Content: "invoices", Visibility: "team", Data: map[string]any{
			"fields": map[string]any{"id": map[string]any{"type": "string", "required": true}, "customer_id": map[string]any{"type": "string"},
				"amount": map[string]any{"type": "number"}, "status": map[string]any{"type": "string"}, "issued_at": map[string]any{"type": "string"}, "due_at": map[string]any{"type": "string"}},
			"unique": []string{"id"}, "indexes": []string{"customer_id", "status"}}}, 0},
		{Record{Kind: "collection", Collection: "support_tickets", Content: "support_tickets", Visibility: "team", Data: map[string]any{
			"fields": map[string]any{"id": map[string]any{"type": "string", "required": true}, "customer_id": map[string]any{"type": "string"},
				"subject": map[string]any{"type": "string"}, "priority": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "opened_at": map[string]any{"type": "string"}},
			"unique": []string{"id"}, "indexes": []string{"customer_id", "priority", "status"}}}, 0},
	}
	owns := func(changes []Change) []Change {
		for i := range changes {
			changes[i].Record.User, changes[i].Record.Workspace = s.user, s.workspace
		}
		return changes
	}
	if _, err := s.engine.Batch(owns(schemas), s.key(r)); err != nil {
		return nil, err
	}
	tables := map[string][]SyncItem{
		"customers": {
			row("c-100", map[string]any{"name": "Rani Textiles", "region": "Tamil Nadu", "plan": "scale", "mrr": json.Number("48000"), "owner": "meera"}),
			row("c-101", map[string]any{"name": "Coromandel Coffee", "region": "Andhra Pradesh", "plan": "growth", "mrr": json.Number("12500"), "owner": "arun"}),
			row("c-102", map[string]any{"name": "Deccan Logistics", "region": "Telangana", "plan": "scale", "mrr": json.Number("91000"), "owner": "meera"}),
			row("c-103", map[string]any{"name": "Kaaveri Dairy", "region": "Karnataka", "plan": "starter", "mrr": json.Number("4200"), "owner": "nadha"}),
			row("c-104", map[string]any{"name": "Thar Solar", "region": "Rajasthan", "plan": "growth", "mrr": json.Number("23000"), "owner": "nadha"}),
			row("c-105", map[string]any{"name": "Sundarban Honey", "region": "West Bengal", "plan": "starter", "mrr": json.Number("6100"), "owner": "arun"}),
		},
		"subscriptions": {
			row("s-1", map[string]any{"customer_id": "c-100", "plan": "scale", "seats": json.Number("40"), "status": "active", "renews_at": "2026-12-01"}),
			row("s-2", map[string]any{"customer_id": "c-101", "plan": "growth", "seats": json.Number("12"), "status": "active", "renews_at": "2026-11-18"}),
			row("s-3", map[string]any{"customer_id": "c-102", "plan": "scale", "seats": json.Number("85"), "status": "active", "renews_at": "2027-01-09"}),
			row("s-4", map[string]any{"customer_id": "c-103", "plan": "starter", "seats": json.Number("4"), "status": "past_due", "renews_at": "2026-10-30"}),
			row("s-5", map[string]any{"customer_id": "c-104", "plan": "growth", "seats": json.Number("18"), "status": "active", "renews_at": "2026-12-22"}),
			row("s-6", map[string]any{"customer_id": "c-105", "plan": "starter", "seats": json.Number("3"), "status": "cancelled", "renews_at": "2026-09-14"}),
		},
		"invoices": {
			row("i-900", map[string]any{"customer_id": "c-100", "amount": json.Number("48000"), "status": "paid", "issued_at": "2026-10-01", "due_at": "2026-10-15"}),
			row("i-901", map[string]any{"customer_id": "c-101", "amount": json.Number("12500"), "status": "paid", "issued_at": "2026-10-01", "due_at": "2026-10-15"}),
			row("i-902", map[string]any{"customer_id": "c-102", "amount": json.Number("91000"), "status": "open", "issued_at": "2026-10-05", "due_at": "2026-10-20"}),
			row("i-903", map[string]any{"customer_id": "c-103", "amount": json.Number("4200"), "status": "overdue", "issued_at": "2026-09-05", "due_at": "2026-09-20"}),
			row("i-904", map[string]any{"customer_id": "c-104", "amount": json.Number("23000"), "status": "paid", "issued_at": "2026-10-01", "due_at": "2026-10-16"}),
			row("i-905", map[string]any{"customer_id": "c-105", "amount": json.Number("6100"), "status": "void", "issued_at": "2026-08-01", "due_at": "2026-08-16"}),
			row("i-906", map[string]any{"customer_id": "c-100", "amount": json.Number("48000"), "status": "open", "issued_at": "2026-11-01", "due_at": "2026-11-16"}),
			row("i-907", map[string]any{"customer_id": "c-102", "amount": json.Number("91000"), "status": "open", "issued_at": "2026-11-01", "due_at": "2026-11-16"}),
		},
		"support_tickets": {
			row("t-700", map[string]any{"customer_id": "c-102", "subject": "export stuck on 85 seats", "priority": "high", "status": "open", "opened_at": "2026-10-28"}),
			row("t-701", map[string]any{"customer_id": "c-103", "subject": "invoice never arrived", "priority": "high", "status": "open", "opened_at": "2026-10-21"}),
			row("t-702", map[string]any{"customer_id": "c-100", "subject": "add Rajasthan warehouse", "priority": "medium", "status": "waiting", "opened_at": "2026-10-19"}),
			row("t-703", map[string]any{"customer_id": "c-101", "subject": "API rate limit questions", "priority": "low", "status": "closed", "opened_at": "2026-10-11"}),
			row("t-704", map[string]any{"customer_id": "c-104", "subject": "double charged in September", "priority": "high", "status": "closed", "opened_at": "2026-10-04"}),
			row("t-705", map[string]any{"customer_id": "c-105", "subject": "wants to resume after cancellation", "priority": "medium", "status": "open", "opened_at": "2026-10-30"}),
		},
	}
	names := []string{"customers", "invoices", "support_tickets", "subscriptions"}
	counts := map[string]any{}
	for _, table := range names {
		changes, c, err := s.syncChanges("acme-sample", SyncInput{Collection: table, KeyField: "id", Items: tables[table], Source: "sample/" + table + ".csv"})
		if err != nil {
			return nil, err
		}
		if _, err = s.engine.Batch(changes, s.key(r)+":"+table); err != nil {
			return nil, err
		}
		counts[table] = c["created"]
	}
	docs := []Change{}
	for _, d := range []struct{ title, text string }{
		{"renewal terms", "Rani Textiles renews annually on the first of December.\nThe contract allows an unlimited-term renewal with sixty days notice to change terms."},
		{"call notes", "Deccan Logistics wants the export fixed before the next board review.\nThey threatened to move to a competitor if seat limits stay in place."},
		{"incident report", "On the fifth of October the export worker retried for nine hours.\nThe stuck export affected Deccan Logistics and Rani Textiles only."},
	} {
		offset := 0
		for _, para := range strings.Split(d.text, "\n\n") {
			for _, line := range strings.Split(para, "\n") {
				if strings.TrimSpace(line) != "" {
					docs = append(docs, Change{Record{Kind: "wiki", Content: line, Source: "sample/" + strings.ReplaceAll(d.title, " ", "-") + ".md",
						Visibility: "team", Metadata: map[string]string{"title": d.title, "byte_offset": strconv.Itoa(offset), "method": "verbatim paragraph"}}, 0})
				}
				offset += len(line) + 1
			}
		}
	}
	if _, err := s.engine.Batch(owns(docs), s.key(r)+":wiki"); err != nil {
		return nil, err
	}
	records, err := s.ingest(context.Background(), Ingest{
		Source: "sample/support-thread.json",
		Messages: []Message{
			{"user", "Deccan Logistics is on the scale plan with 85 seats and they renew in January."},
			{"assistant", "Noted. I will flag the stuck export before the renewal."},
			{"user", "Their invoice i-902 for ninety-one thousand is still open."},
		},
		Facts: []Fact{
			{Subject: "customer", Predicate: "renews_in", Value: "January", Quote: "they renew in January", MessageIndex: 0, Confidence: 0.9},
			{Subject: "customer", Predicate: "seats", Value: "85", Quote: "85 seats", MessageIndex: 0, Confidence: 0.95},
		},
	}, s.key(r)+":chat")
	if err != nil {
		return nil, err
	}
	return map[string]any{"seeded": true, "rows": counts, "documents": len(docs), "memories": len(records),
		"sources": s.listSources(),
		"next": []string{
			"mdb search \"renewal\"",
			"mdb query customers --where region=\"Rajasthan\"",
			"mdb collections create --help",
			"open the console and click graph",
		}}, nil
}

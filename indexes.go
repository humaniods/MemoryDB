package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Derived state is everything the engine keeps beside the journal so a read does not have to
// recompute it: collection schemas, equality indexes, unique-key holders and per-record term
// statistics for BM25. rebuildIndexes derives all of it from the records at once (on open,
// after compaction and after any schema change); a batch that touches no collection updates
// only the records it wrote, so a write costs the size of the write rather than the size of
// the database.

// idSet is the set of record ids that share one indexed value.
type idSet map[string]struct{}

// termStats is the tokenized form of one record's content, kept so a search counts term
// frequencies with map lookups instead of re-tokenizing every record on every query.
type termStats struct {
	counts map[string]int
	length int
}

func statsOf(content string) termStats {
	ts := tokens(content)
	counts := make(map[string]int, len(ts))
	for _, t := range ts {
		counts[t]++
	}
	return termStats{counts: counts, length: len(ts)}
}

func indexValue(v any) string {
	if n, ok := number(v); ok {
		return "number:" + n.RatString()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// uniqueValue is the canonical form two documents must not share in a unique field: exact
// numbers compare by value, casefold strings ignore case, everything else by its JSON.
func uniqueValue(v any, field Field) string {
	if n, ok := number(v); ok {
		return "number:" + n.RatString()
	}
	if text, isText := v.(string); isText && field.Casefold {
		return "fold:" + strings.ToLower(text)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func indexedFields(schema Schema) []string {
	return append(append([]string{}, schema.Indexes...), schema.Unique...)
}

func (e *Engine) rebuildIndexes() {
	e.schemas = map[string]Schema{}
	e.indexes = map[string]map[string]idSet{}
	e.unique = map[string]map[string]idSet{}
	e.terms = make(map[string]termStats, len(e.records))
	for _, h := range e.records {
		r := h[len(h)-1]
		if r.Kind != "collection" || r.Deleted {
			continue
		}
		schema, err := schemaOf(r)
		if err != nil {
			continue
		}
		base := r.Workspace + "\x00" + r.Collection
		e.schemas[base] = schema
		for _, field := range indexedFields(schema) {
			e.indexes[base+"\x00"+field] = map[string]idSet{}
		}
		for _, field := range schema.Unique {
			e.unique[base+"\x00"+field] = map[string]idSet{}
		}
	}
	for id, h := range e.records {
		e.indexRecord(id, h[len(h)-1], true)
	}
}

// indexRecord adds a record's latest version to the derived state, or removes it when add is
// false. Deleted versions are never indexed, so removing one is a no-op.
func (e *Engine) indexRecord(id string, r Record, add bool) {
	if r.Deleted {
		return
	}
	if add {
		e.terms[id] = statsOf(r.Content)
	} else {
		delete(e.terms, id)
	}
	if r.Kind != "document" {
		return
	}
	base := r.Workspace + "\x00" + r.Collection
	schema, ok := e.schemas[base]
	if !ok {
		return
	}
	for _, field := range indexedFields(schema) {
		if v, ok := valueAt(r.Data, field); ok {
			bucket(e.indexes[base+"\x00"+field], indexValue(v), id, add)
		}
	}
	for _, field := range schema.Unique {
		if v, ok := valueAt(r.Data, field); ok && v != nil {
			bucket(e.unique[base+"\x00"+field], uniqueValue(v, schema.Fields[field]), id, add)
		}
	}
}

func bucket(idx map[string]idSet, value, id string, add bool) {
	if idx == nil {
		return
	}
	if add {
		if idx[value] == nil {
			idx[value] = idSet{}
		}
		idx[value][id] = struct{}{}
		return
	}
	delete(idx[value], id)
	if len(idx[value]) == 0 {
		delete(idx, value)
	}
}

// validateBatch checks a batch that is already applied to e.records. A batch that writes a
// collection can change what every row of it must satisfy, so it re-checks everything; any
// other batch checks only the documents it wrote, against the cached schema and the unique
// holders index. Rows nobody touched were valid when they were written, and the only thing
// time can do to them is expire them, which removes constraints rather than adding any.
func (e *Engine) validateBatch(prepared []Record, touched map[string]bool, now time.Time) error {
	for _, r := range prepared {
		if r.Kind == "collection" {
			return validateCollections(e.records, now)
		}
	}
	claimed := map[string]map[string]bool{}
	for _, r := range prepared {
		if r.Kind != "document" || !active(r, now) {
			continue
		}
		base := r.Workspace + "\x00" + r.Collection
		schema, ok := e.schemas[base]
		if !ok {
			return fmt.Errorf("collection %s does not exist", r.Collection)
		}
		if err := checkDocument(r, schema); err != nil {
			return err
		}
		for _, path := range schema.Unique {
			v, ok := valueAt(r.Data, path)
			if !ok || v == nil {
				continue
			}
			canonical := uniqueValue(v, schema.Fields[path])
			uk := base + "\x00" + path
			if claimed[uk] == nil {
				claimed[uk] = map[string]bool{}
			}
			if claimed[uk][canonical] {
				return fmt.Errorf("%w: unique field %s", ErrConflict, path)
			}
			claimed[uk][canonical] = true
			// Holders written by this batch are judged by their new value, above; the index
			// still describes their previous one.
			for holder := range e.unique[uk][canonical] {
				if touched[holder] {
					continue
				}
				if h := e.records[holder]; len(h) > 0 && active(h[len(h)-1], now) {
					return fmt.Errorf("%w: unique field %s", ErrConflict, path)
				}
			}
		}
	}
	return nil
}

package main

import (
	"encoding/json"
)

func indexValue(v any) string {
	if n, ok := number(v); ok {
		return "number:" + n.RatString()
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func (e *Engine) rebuildIndexes() {
	e.indexes = map[string]map[string][]string{}
	for _, h := range e.records {
		r := h[len(h)-1]
		if r.Kind != "collection" || r.Deleted {
			continue
		}
		schema, err := schemaOf(r)
		if err != nil {
			continue
		}
		for _, field := range append(append([]string{}, schema.Indexes...), schema.Unique...) {
			e.indexes[r.Workspace+"\x00"+r.Collection+"\x00"+field] = map[string][]string{}
		}
	}
	for id, h := range e.records {
		r := h[len(h)-1]
		if r.Kind != "document" || r.Deleted {
			continue
		}
		schemaKey := r.Workspace + "\x00" + r.Collection + "\x00"
		for key, idx := range e.indexes {
			if len(key) > len(schemaKey) && key[:len(schemaKey)] == schemaKey {
				field := key[len(schemaKey):]
				if v, ok := valueAt(r.Data, field); ok {
					k := indexValue(v)
					idx[k] = append(idx[k], id)
				}
			}
		}
	}
}

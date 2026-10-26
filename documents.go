package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var nameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func validName(s string) bool { return nameRE.MatchString(s) }

type Field struct {
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}
type Schema struct {
	Indexes []string         `json:"indexes,omitempty"`
	Fields  map[string]Field `json:"fields"`
	Unique  []string         `json:"unique,omitempty"`
	Strict  bool             `json:"strict,omitempty"`
}

func schemaOf(r Record) (Schema, error) {
	var s Schema
	b, err := json.Marshal(r.Data)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}
func valueAt(data map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var v any = data
	for _, p := range parts {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return v, true
}
func number(v any) (*big.Rat, bool) {
	var s string
	switch x := v.(type) {
	case json.Number:
		s = string(x)
	case float64:
		s = strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		s = strconv.Itoa(x)
	case int64:
		s = strconv.FormatInt(x, 10)
	default:
		return nil, false
	}
	if len(s) > 128 {
		return nil, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		ex, err := strconv.Atoi(s[i+1:])
		if err != nil || ex > 308 || ex < -308 {
			return nil, false
		}
	}
	r, ok := new(big.Rat).SetString(s)
	return r, ok
}
func equal(a, b any) bool {
	if x, ok := number(a); ok {
		y, ok := number(b)
		return ok && x.Cmp(y) == 0
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
func typeOK(v any, t string) bool {
	switch t {
	case "any":
		return true
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := number(v)
		return ok
	case "integer":
		n, ok := number(v)
		return ok && n.IsInt()
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	}
	return false
}
func validateCollections(all map[string][]Record, now time.Time) error {
	schemas := map[string]Schema{}
	for _, h := range all {
		r := h[len(h)-1]
		if r.Kind != "collection" || !active(r, now) {
			continue
		}
		if !validName(r.Collection) {
			return errors.New("invalid collection name")
		}
		key := r.Workspace + "\x00" + r.Collection
		if _, ok := schemas[key]; ok {
			return ErrConflict
		}
		schema, err := schemaOf(r)
		if err != nil {
			return err
		}
		for name, field := range schema.Fields {
			if name == "" {
				return errors.New("empty field name")
			}
			switch field.Type {
			case "any", "string", "number", "integer", "boolean", "object", "array":
			default:
				return errors.New("unsupported schema type")
			}
		}
		for _, path := range append(append([]string{}, schema.Unique...), schema.Indexes...) {
			if _, ok := schema.Fields[path]; !ok {
				return errors.New("unique field must have a schema declaration")
			}
		}
		schemas[key] = schema
	}
	unique := map[string]map[string]bool{}
	for _, h := range all {
		r := h[len(h)-1]
		if r.Kind != "document" || !active(r, now) {
			continue
		}
		key := r.Workspace + "\x00" + r.Collection
		schema, ok := schemas[key]
		if !ok {
			return fmt.Errorf("collection %s does not exist", r.Collection)
		}
		for path, field := range schema.Fields {
			v, ok := valueAt(r.Data, path)
			if !ok {
				if field.Required {
					return fmt.Errorf("required field missing: %s", path)
				}
				continue
			}
			if !typeOK(v, field.Type) {
				return fmt.Errorf("invalid type for field %s", path)
			}
		}
		if schema.Strict {
			for k := range r.Data {
				declared := false
				for path := range schema.Fields {
					if strings.Split(path, ".")[0] == k {
						declared = true
						break
					}
				}
				if !declared {
					return fmt.Errorf("undeclared field: %s", k)
				}
			}
		}
		for _, path := range schema.Unique {
			v, ok := valueAt(r.Data, path)
			if !ok || v == nil {
				continue
			}
			b, _ := json.Marshal(v)
			canonical := string(b)
			if n, ok := number(v); ok {
				canonical = "number:" + n.RatString()
			}
			uk := key + "\x00" + path
			if unique[uk] == nil {
				unique[uk] = map[string]bool{}
			}
			if unique[uk][canonical] {
				return fmt.Errorf("%w: unique field %s", ErrConflict, path)
			}
			unique[uk][canonical] = true
		}
	}
	return nil
}

type Predicate struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}
type DocumentQuery struct {
	Where      []Predicate `json:"where,omitempty"`
	OrderBy    string      `json:"order_by,omitempty"`
	Descending bool        `json:"descending,omitempty"`
	Limit      int         `json:"limit,omitempty"`
	Cursor     string      `json:"cursor,omitempty"`
	Projection []string    `json:"projection,omitempty"`
}
type DocumentPage struct {
	Plan     string   `json:"plan"`
	Results  []Record `json:"results"`
	Total    int      `json:"total"`
	Next     string   `json:"next_cursor,omitempty"`
	Sequence uint64   `json:"sequence"`
}

func compare(a, b any) (int, bool) {
	if x, ok := number(a); ok {
		y, ok := number(b)
		if !ok {
			return 0, false
		}
		return x.Cmp(y), true
	}
	x, ok := a.(string)
	y, ok2 := b.(string)
	if ok && ok2 {
		return strings.Compare(x, y), true
	}
	return 0, false
}

// orderValue provides a total order even for schemaless heterogeneous fields.
func orderValue(a, b any) int {
	rank := func(v any) int {
		if v == nil {
			return 0
		}
		if _, ok := v.(bool); ok {
			return 1
		}
		if _, ok := number(v); ok {
			return 2
		}
		if _, ok := v.(string); ok {
			return 3
		}
		return 4
	}
	ar, br := rank(a), rank(b)
	if ar < br {
		return -1
	}
	if ar > br {
		return 1
	}
	if c, ok := compare(a, b); ok {
		return c
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return strings.Compare(string(ab), string(bb))
}
func validateQuery(q DocumentQuery) error {
	if q.Limit < 0 || q.Limit > 500 || len(q.Where) > 32 || len(q.Projection) > 64 {
		return errors.New("query limits exceeded")
	}
	for _, p := range q.Where {
		if p.Field == "" {
			return errors.New("predicate field required")
		}
		switch p.Op {
		case "eq", "ne", "gt", "gte", "lt", "lte", "contains", "prefix":
		case "in":
			if _, ok := p.Value.([]any); !ok {
				return errors.New("in requires array")
			}
		case "exists":
			if _, ok := p.Value.(bool); !ok {
				return errors.New("exists requires boolean")
			}
		default:
			return fmt.Errorf("unsupported operator %s", p.Op)
		}
	}
	return nil
}
func matches(data map[string]any, ps []Predicate) bool {
	for _, p := range ps {
		v, exists := valueAt(data, p.Field)
		ok := false
		switch p.Op {
		case "exists":
			ok = exists == p.Value.(bool)
		case "eq":
			ok = exists && equal(v, p.Value)
		case "ne":
			ok = exists && !equal(v, p.Value)
		case "gt", "gte", "lt", "lte":
			if c, compatible := compare(v, p.Value); exists && compatible {
				switch p.Op {
				case "gt":
					ok = c > 0
				case "gte":
					ok = c >= 0
				case "lt":
					ok = c < 0
				case "lte":
					ok = c <= 0
				}
			}
		case "in":
			for _, x := range p.Value.([]any) {
				if exists && equal(v, x) {
					ok = true
					break
				}
			}
		case "contains":
			switch x := v.(type) {
			case string:
				needle, isString := p.Value.(string)
				ok = isString && strings.Contains(x, needle)
			case []any:
				for _, x := range x {
					if equal(x, p.Value) {
						ok = true
						break
					}
				}
			}
		case "prefix":
			x, a := v.(string)
			prefix, b := p.Value.(string)
			ok = a && b && strings.HasPrefix(x, prefix)
		}
		if !ok {
			return false
		}
	}
	return true
}
func (e *Engine) Documents(user, workspace, collection string, q DocumentQuery) (DocumentPage, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	page := DocumentPage{Results: []Record{}, Sequence: e.sequence}
	if err := validateQuery(q); err != nil {
		return page, err
	}
	offset := 0
	cursorLive := ""
	// Cursor binds to revision, scope, and query shape; concurrent writes force restart.
	fingerprintQ := q
	fingerprintQ.Cursor = ""
	fingerprint, _ := json.Marshal([]any{user, workspace, collection, fingerprintQ})
	if q.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil {
			return page, errors.New("invalid cursor")
		}
		var c struct {
			Sequence uint64
			Offset   int
			Query    string
			Live     string
		}
		if json.Unmarshal(b, &c) != nil || c.Offset < 0 || c.Query != string(fingerprint) {
			return page, errors.New("cursor does not match query")
		}
		if c.Sequence != e.sequence {
			return page, ErrConflict
		}
		offset = c.Offset
		cursorLive = c.Live
	}
	all := []Record{}
	now := time.Now()
	page.Plan = "scan"
	var candidates map[string]bool
	for _, p := range q.Where {
		if p.Op == "eq" {
			if idx, ok := e.indexes[workspace+"\x00"+collection+"\x00"+p.Field]; ok {
				candidates = map[string]bool{}
				for _, id := range idx[indexValue(p.Value)] {
					candidates[id] = true
				}
				page.Plan = "equality index: " + p.Field
				break
			}
		}
	}
	rows := e.records
	if candidates != nil {
		rows = make(map[string][]Record, len(candidates))
		for id := range candidates {
			rows[id] = e.records[id]
		}
	}
	for _, h := range rows {
		r := h[len(h)-1]
		if r.Kind == "document" && r.Collection == collection && active(r, now) && visible(r, user, workspace) && matches(r.Data, q.Where) {
			all = append(all, r)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if q.OrderBy != "" {
			a, ae := valueAt(all[i].Data, q.OrderBy)
			b, be := valueAt(all[j].Data, q.OrderBy)
			if ae != be {
				return ae
			}
			if c := orderValue(a, b); c != 0 {
				if q.Descending {
					return c > 0
				}
				return c < 0
			}
		}
		return all[i].ID < all[j].ID
	})
	liveIDs := make([]string, len(all))
	for i, r := range all {
		liveIDs[i] = r.ID
	}
	live := hashText(strings.Join(liveIDs, ","))
	if q.Cursor != "" && cursorLive != live {
		return page, ErrConflict
	}
	page.Total = len(all)
	limit := q.Limit
	if limit == 0 {
		limit = 50
	}
	if offset > len(all) {
		return page, errors.New("cursor offset out of range")
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	for _, r := range all[offset:end] {
		r = clone(r)
		if len(q.Projection) > 0 {
			data := map[string]any{}
			for _, p := range q.Projection {
				if v, ok := valueAt(r.Data, p); ok {
					data[p] = v
				}
			}
			r.Data = data
		}
		page.Results = append(page.Results, r)
	}
	if end < len(all) {
		b, _ := json.Marshal(struct {
			Sequence uint64
			Offset   int
			Query    string
			Live     string
		}{e.sequence, end, string(fingerprint), live})
		page.Next = base64.RawURLEncoding.EncodeToString(b)
	}
	return page, nil
}
func (e *Engine) Collections(workspace string) []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rs := []Record{}
	for _, h := range e.records {
		r := h[len(h)-1]
		if r.Kind == "collection" && r.Workspace == workspace && active(r, time.Now()) {
			rs = append(rs, clone(r))
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Collection < rs[j].Collection })
	return rs
}

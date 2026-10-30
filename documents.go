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
	// Casefold makes a unique index ignore ASCII case, the way SQLite's
	// UNIQUE COLLATE NOCASE does, so Priya@x.com and priya@x.com are one identity.
	// It changes uniqueness only; eq stays case-exact and iequals is the folding match.
	Casefold bool `json:"casefold,omitempty"`
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
			if field.Casefold && field.Type != "string" {
				return errors.New("casefold requires a string field")
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
			} else if text, isText := v.(string); isText && schema.Fields[path].Casefold {
				canonical = "fold:" + strings.ToLower(text)
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
type OrderTerm struct {
	Field      string `json:"field"`
	Descending bool   `json:"descending,omitempty"`
}

// Order accepts either "order_by":"age" or
// "order_by":[{"field":"likes","descending":true},{"field":"created_at","descending":true}],
// so an existing single-field caller keeps working unchanged.
type Order []OrderTerm

func (o *Order) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		if single == "" {
			*o = Order{}
			return nil
		}
		*o = Order{{Field: single}}
		return nil
	}
	var terms []OrderTerm
	if err := json.Unmarshal(b, &terms); err != nil {
		return err
	}
	*o = terms
	return nil
}

// terms folds the legacy top-level descending flag onto a single-field sort, which is
// the only shape that flag was ever defined for.
func (q DocumentQuery) terms() []OrderTerm {
	if len(q.OrderBy) != 1 || !q.Descending {
		return q.OrderBy
	}
	return []OrderTerm{{Field: q.OrderBy[0].Field, Descending: true}}
}

type Aggregation struct {
	Fn    string `json:"fn"`
	Field string `json:"field"`
	As    string `json:"as,omitempty"`
}

type AggregateGroup struct {
	Group  map[string]any `json:"group"`
	Count  int            `json:"count"`
	Values map[string]any `json:"values"`
}

type DocumentQuery struct {
	Where []Predicate `json:"where,omitempty"`
	// Or groups are each ANDed like Where, and a record matches when Where passes and
	// at least one group passes. Not excludes records that match any of its predicates.
	Or         [][]Predicate `json:"or,omitempty"`
	Not        []Predicate   `json:"not,omitempty"`
	OrderBy    Order         `json:"order_by,omitempty"`
	Descending bool          `json:"descending,omitempty"`
	Limit      int           `json:"limit,omitempty"`
	Offset     int           `json:"offset,omitempty"`
	Cursor     string        `json:"cursor,omitempty"`
	Projection []string      `json:"projection,omitempty"`
	// Aggregate and GroupBy collapse the whole filtered set, not the current page.
	Aggregate []Aggregation `json:"aggregate,omitempty"`
	GroupBy   []string      `json:"group_by,omitempty"`
}
type DocumentPage struct {
	Plan     string           `json:"plan"`
	Results  []Record         `json:"results"`
	Total    int              `json:"total"`
	Next     string           `json:"next_cursor,omitempty"`
	Sequence uint64           `json:"sequence"`
	Groups   []AggregateGroup `json:"groups,omitempty"`
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
	if len(q.Or) > 32 || len(q.Not) > 32 {
		return errors.New("query limits exceeded")
	}
	groups := [][]Predicate{q.Where, q.Not}
	for _, group := range q.Or {
		if len(group) == 0 || len(group) > 32 {
			return errors.New("each or group needs one to thirty-two predicates")
		}
		groups = append(groups, group)
	}
	for _, ps := range groups {
		for _, p := range ps {
			if err := validatePredicate(p); err != nil {
				return err
			}
		}
	}
	if q.Offset < 0 || q.Offset > 1_000_000 {
		return errors.New("offset out of range")
	}
	if q.Offset > 0 && q.Cursor != "" {
		return errors.New("offset and cursor cannot be combined")
	}
	terms := q.terms()
	if len(terms) > 4 {
		return errors.New("too many sort fields")
	}
	if len(terms) > 1 && q.Descending {
		return errors.New("descending applies to a single order_by field; set it per term")
	}
	sortedBy := map[string]bool{}
	for _, t := range terms {
		if t.Field == "" {
			return errors.New("sort field required")
		}
		if sortedBy[t.Field] {
			return fmt.Errorf("duplicate sort field %s", t.Field)
		}
		sortedBy[t.Field] = true
	}
	return validateAggregates(q)
}

func validatePredicate(p Predicate) error {
	if p.Field == "" {
		return errors.New("predicate field required")
	}
	switch p.Op {
	case "eq", "ne", "gt", "gte", "lt", "lte", "contains", "icontains", "prefix", "iequals":
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
		case "contains", "icontains":
			fold := p.Op == "icontains"
			switch x := v.(type) {
			case string:
				needle, isString := p.Value.(string)
				if isString && fold {
					ok = strings.Contains(strings.ToLower(x), strings.ToLower(needle))
				} else {
					ok = isString && strings.Contains(x, needle)
				}
			case []any:
				// Arrays keep membership semantics; only the comparison folds case.
				for _, item := range x {
					if fold {
						s1, a := item.(string)
						s2, b := p.Value.(string)
						ok = a && b && strings.EqualFold(s1, s2)
					} else {
						ok = equal(item, p.Value)
					}
					if ok {
						break
					}
				}
			}
		case "prefix":
			x, a := v.(string)
			prefix, b := p.Value.(string)
			ok = a && b && strings.HasPrefix(x, prefix)
		case "iequals":
			x, a := v.(string)
			needle, b := p.Value.(string)
			ok = a && b && exists && strings.EqualFold(x, needle)
		}
		if !ok {
			return false
		}
	}
	return true
}

// matchesQuery applies Where, then the Or groups, then Not.
func matchesQuery(data map[string]any, q DocumentQuery) bool {
	if !matches(data, q.Where) {
		return false
	}
	if len(q.Or) > 0 {
		any := false
		for _, group := range q.Or {
			if matches(data, group) {
				any = true
				break
			}
		}
		if !any {
			return false
		}
	}
	for _, p := range q.Not {
		if matches(data, []Predicate{p}) {
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
	// Page numbers skip straight to a row without a cursor's promise that nothing wrote
	// in between, which a public listing can never keep.
	if q.Offset > 0 {
		offset = q.Offset
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
		if r.Kind == "document" && r.Collection == collection && active(r, now) && visible(r, user, workspace) && matchesQuery(r.Data, q) {
			all = append(all, r)
		}
	}
	// Totals cover every filtered record, not just the page that comes back.
	page.Groups = aggregate(all, q)
	terms := q.terms()
	sort.Slice(all, func(i, j int) bool {
		for _, t := range terms {
			a, ae := valueAt(all[i].Data, t.Field)
			b, be := valueAt(all[j].Data, t.Field)
			if ae != be {
				return ae
			}
			if c := orderValue(a, b); c != 0 {
				if t.Descending {
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
		// A page number past the last row is a legitimate question with an empty answer,
		// which is how Postgres and SQLite treat LIMIT/OFFSET. A cursor is different: it
		// claims a position the sequence said was still there, so trusting it would hide a
		// bug rather than report one.
		if q.Offset > 0 {
			offset = len(all)
		} else {
			return page, errors.New("cursor offset out of range")
		}
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

var aggFns = map[string]bool{"sum": true, "avg": true, "min": true, "max": true}

func aggName(a Aggregation) (string, error) {
	name := a.As
	if name == "" {
		name = a.Fn + "_" + strings.NewReplacer(".", "_", "-", "_").Replace(a.Field)
	}
	if !validName(name) {
		return "", fmt.Errorf("invalid aggregate output name %q", name)
	}
	return name, nil
}

func validateAggregates(q DocumentQuery) error {
	if len(q.Aggregate) > 16 || len(q.GroupBy) > 8 {
		return errors.New("aggregate limits exceeded")
	}
	seen := map[string]bool{}
	for _, a := range q.Aggregate {
		if !aggFns[a.Fn] {
			return fmt.Errorf("unsupported aggregate %s", a.Fn)
		}
		if a.Field == "" {
			return errors.New("aggregate field required")
		}
		name, err := aggName(a)
		if err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("duplicate aggregate %s", name)
		}
		seen[name] = true
	}
	for _, f := range q.GroupBy {
		if f == "" {
			return errors.New("group field required")
		}
	}
	return nil
}

func rationalJSON(v *big.Rat) json.Number {
	if v.IsInt() {
		return json.Number(v.Num().String())
	}
	// A denominator of only 2s and 5s has an exact finite decimal, so an average is
	// printed in full instead of being rounded through a float. Others get 17 digits.
	rem := new(big.Int).Set(v.Denom())
	twos, fives := 0, 0
	for rem.Bit(0) == 0 {
		rem.Div(rem, big.NewInt(2))
		twos++
	}
	five := big.NewInt(5)
	for rem.Sign() != 0 && new(big.Int).Mod(rem, five).Sign() == 0 {
		rem.Div(rem, five)
		fives++
	}
	if rem.Sign() == 1 {
		places := twos
		if fives > places {
			places = fives
		}
		return json.Number(v.FloatString(places))
	}
	return json.Number(new(big.Float).SetPrec(128).SetRat(v).Text('g', 17))
}

// accumulator folds records into aggregate values in one pass. sum and avg skip
// non-numeric values rather than failing, the way SQL SUM ignores NULLs, and report
// null when a group holds no number at all.
type accumulator struct {
	q      DocumentQuery
	sums   map[string]*big.Rat
	counts map[string]int
	picks  map[string]any
	total  int
}

func newAccumulator(q DocumentQuery) *accumulator {
	return &accumulator{q: q, sums: map[string]*big.Rat{}, counts: map[string]int{}, picks: map[string]any{}}
}

func (a *accumulator) add(data map[string]any) {
	a.total++
	for _, ag := range a.q.Aggregate {
		name, err := aggName(ag)
		if err != nil {
			continue
		}
		v, exists := valueAt(data, ag.Field)
		if !exists {
			continue
		}
		switch ag.Fn {
		case "sum", "avg":
			n, ok := number(v)
			if !ok {
				continue
			}
			if a.sums[name] == nil {
				a.sums[name] = new(big.Rat)
			}
			a.sums[name].Add(a.sums[name], n)
			a.counts[name]++
		case "min", "max":
			cur, have := a.picks[name]
			if !have {
				a.picks[name] = v
				continue
			}
			c := orderValue(v, cur)
			if (ag.Fn == "min" && c < 0) || (ag.Fn == "max" && c > 0) {
				a.picks[name] = v
			}
		}
	}
}

func (a *accumulator) result() map[string]any {
	out := map[string]any{}
	for _, ag := range a.q.Aggregate {
		name, err := aggName(ag)
		if err != nil {
			continue
		}
		switch ag.Fn {
		case "sum":
			if s := a.sums[name]; s != nil {
				out[name] = rationalJSON(s)
			} else {
				out[name] = nil
			}
		case "avg":
			s, c := a.sums[name], a.counts[name]
			if s != nil && c > 0 {
				out[name] = rationalJSON(new(big.Rat).Quo(s, new(big.Rat).SetInt64(int64(c))))
			} else {
				out[name] = nil
			}
		default:
			out[name] = a.picks[name]
		}
	}
	return out
}

// aggregate folds the full filtered record set, so pagination never skews a total.
// Groups come back ordered by their encoded key, keeping repeated queries byte-identical.
func aggregate(rows []Record, q DocumentQuery) []AggregateGroup {
	if len(q.Aggregate) == 0 && len(q.GroupBy) == 0 {
		return nil
	}
	if len(q.GroupBy) == 0 {
		acc := newAccumulator(q)
		for _, r := range rows {
			acc.add(r.Data)
		}
		return []AggregateGroup{{Count: acc.total, Values: acc.result()}}
	}
	type bucket struct {
		group map[string]any
		acc   *accumulator
	}
	buckets := map[string]*bucket{}
	keys := []string{}
	for _, r := range rows {
		parts := make([]any, 0, len(q.GroupBy))
		group := map[string]any{}
		for _, f := range q.GroupBy {
			v, _ := valueAt(r.Data, f)
			group[f] = v
			parts = append(parts, v)
		}
		encoded, err := json.Marshal(parts)
		key := ""
		if err == nil {
			key = string(encoded)
		}
		if _, ok := buckets[key]; !ok {
			buckets[key] = &bucket{group: group, acc: newAccumulator(q)}
			keys = append(keys, key)
		}
		buckets[key].acc.add(r.Data)
	}
	sort.Strings(keys)
	out := make([]AggregateGroup, 0, len(keys))
	for _, key := range keys {
		b := buckets[key]
		out = append(out, AggregateGroup{Group: b.group, Count: b.acc.total, Values: b.acc.result()})
	}
	return out
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

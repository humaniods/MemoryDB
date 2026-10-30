package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The engine is original code. No Tencent/Hy packages or external database.
// One OS process owns a journal. Updates are durable before becoming visible.
type Record struct {
	Collection     string            `json:"collection,omitempty"`
	Data           map[string]any    `json:"data"`
	Visibility     string            `json:"visibility,omitempty"`
	Readers        []string          `json:"readers,omitempty"`
	Agents         []string          `json:"agents,omitempty"`
	EmbeddingModel string            `json:"embedding_model,omitempty"`
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Content        string            `json:"content"`
	User           string            `json:"user"`
	Workspace      string            `json:"workspace"`
	Source         string            `json:"source"`
	Layer          string            `json:"layer"`
	Parent         string            `json:"parent,omitempty"`
	Links          []string          `json:"links,omitempty"`
	Vector         []float64         `json:"vector,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Expires        *time.Time        `json:"expires,omitempty"`
	Version        int               `json:"version"`
	Deleted        bool              `json:"deleted"`
	Created        time.Time         `json:"created"`
}
type Engine struct {
	mu         sync.RWMutex
	file       *os.File
	lock       *os.File
	path       string
	records    map[string][]Record
	receipts   map[string]Receipt
	indexes    map[string]map[string][]string
	poisoned   bool
	closed     bool
	sequence   uint64
	writeFrame func([]byte) (int, error)
	syncFile   func() error
}
type Change struct {
	Record   Record `json:"record"`
	Expected int    `json:"expected"`
}
type Receipt struct {
	Hash    string   `json:"hash"`
	Records []Record `json:"records"`
}
type Event struct {
	Format   int                `json:"format"`
	Records  []Record           `json:"records"`
	Key      string             `json:"key,omitempty"`
	Hash     string             `json:"hash,omitempty"`
	Snapshot bool               `json:"snapshot,omitempty"`
	Receipts map[string]Receipt `json:"receipts,omitempty"`
	Sequence uint64             `json:"sequence"`
}

var ErrConflict = errors.New("version or uniqueness conflict")
var ErrMissing = errors.New("record not found")
var ErrStorage = errors.New("storage unavailable; commit outcome may be unknown")

const maxFrame = 32 << 20

func decodeJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}
func Open(path string) (*Engine, error) {
	lock, err := lockPath(path + ".lock")
	if err != nil {
		return nil, err
	}
	failLock := func(err error) (*Engine, error) { unlockPath(lock); return nil, err }
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return failLock(err)
	}
	fail := func(err error) (*Engine, error) { f.Close(); return failLock(err) }
	e := &Engine{file: f, lock: lock, path: path, records: map[string][]Record{}, receipts: map[string]Receipt{}}
	var offset int64
	for {
		head := make([]byte, 4)
		_, err = io.ReadFull(f, head)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			if err = f.Truncate(offset); err != nil {
				return fail(err)
			}
			break
		}
		if err != nil {
			return fail(err)
		}
		n := binary.BigEndian.Uint32(head)
		if n == 0 || n > maxFrame {
			return fail(fmt.Errorf("invalid frame at %d", offset))
		}
		payload := make([]byte, int(n)+sha256.Size)
		_, err = io.ReadFull(f, payload)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if err = f.Truncate(offset); err != nil {
				return fail(err)
			}
			break
		}
		if err != nil {
			return fail(err)
		}
		sum := sha256.Sum256(payload[:n])
		if !bytes.Equal(sum[:], payload[n:]) {
			return fail(fmt.Errorf("checksum mismatch at %d", offset))
		}
		var event Event
		if err = decodeJSON(payload[:n], &event); err != nil {
			return fail(err)
		}
		if event.Format == 0 {
			var legacy Record
			if err = decodeJSON(payload[:n], &legacy); err != nil {
				return fail(err)
			}
			event = Event{Format: 2, Records: []Record{legacy}, Sequence: e.sequence + 1}
		}
		if event.Format != 2 {
			return fail(errors.New("unsupported storage format"))
		}
		if event.Snapshot {
			if offset != 0 {
				return fail(errors.New("snapshot must be first frame"))
			}
			e.records = map[string][]Record{}
			if event.Receipts != nil {
				e.receipts = event.Receipts
			}
		}
		for _, r := range event.Records {
			if r.ID == "" || r.Version < 1 {
				return fail(errors.New("invalid stored identity"))
			}
			h := e.records[r.ID]
			if len(h) > 0 && r.Version != h[len(h)-1].Version+1 {
				return fail(errors.New("invalid revision sequence"))
			}
			if len(h) == 0 && !event.Snapshot && r.Version != 1 {
				return fail(errors.New("missing revision"))
			}
			e.records[r.ID] = append(h, r)
		}
		if !event.Snapshot && event.Sequence != e.sequence+1 {
			return fail(errors.New("invalid transaction sequence"))
		}
		e.sequence = event.Sequence
		if event.Key != "" {
			e.receipts[event.Key] = Receipt{event.Hash, event.Records}
		}
		offset += 4 + int64(n) + sha256.Size
	}
	if _, err = f.Seek(0, io.SeekEnd); err != nil {
		return fail(err)
	}
	if err = f.Sync(); err != nil {
		return fail(err)
	}
	if err = syncDir(filepath.Dir(path)); err != nil {
		return fail(err)
	}
	e.rebuildIndexes()
	e.writeFrame = f.Write
	e.syncFile = f.Sync
	return e, nil
}
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	err := e.file.Close()
	unlockPath(e.lock)
	return err
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func clone(r Record) Record { b, _ := json.Marshal(r); var c Record; _ = decodeJSON(b, &c); return c }
func cloneRecords(rs []Record) []Record {
	out := make([]Record, len(rs))
	for i, r := range rs {
		out[i] = clone(r)
	}
	return out
}
func active(r Record, now time.Time) bool {
	return !r.Deleted && (r.Expires == nil || r.Expires.After(now))
}
func visible(r Record, user, workspace string) bool {
	if r.Workspace != workspace {
		return false
	}
	return r.User == user || r.Visibility == "team" || contains(r.Readers, user)
}
func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
func valid(r Record) error {
	switch r.Kind {
	case "chat", "fact", "summary", "profile", "intention", "experience", "skill", "wiki", "code", "revision", "document", "collection", "job":
	default:
		return errors.New("unsupported kind")
	}
	if r.User == "" || r.Workspace == "" {
		return errors.New("user and workspace required")
	}
	if r.Kind == "collection" && (r.Expires != nil || r.Visibility != "team") {
		return errors.New("collection schemas must be team-visible and cannot expire")
	}
	if r.Kind == "document" {
		if !validName(r.Collection) || r.Data == nil {
			return errors.New("document requires collection and JSON object data")
		}
	} else if strings.TrimSpace(r.Content) == "" {
		return errors.New("content required")
	}
	if r.Visibility != "" && r.Visibility != "private" && r.Visibility != "team" && r.Visibility != "restricted" {
		return errors.New("invalid visibility")
	}
	if len(r.Vector) > 4096 {
		return errors.New("vector exceeds 4096 dimensions")
	}
	for _, v := range r.Vector {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("vector must be finite")
		}
	}
	if r.Expires != nil && r.Expires.IsZero() {
		return errors.New("invalid expiry")
	}
	return nil
}
func id() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// zeroValue is what a widened schema hands the rows written before a field existed, the
// same job SQLite's ALTER TABLE ADD COLUMN does with its default.
func zeroValue(kind string) any {
	switch kind {
	case "string":
		return ""
	case "number", "integer":
		return json.Number("0")
	case "boolean":
		return false
	case "object":
		return map[string]any{}
	case "array":
		return []any{}
	}
	return nil
}

// expandAlter turns a replaced collection record into the row rewrites that widening means,
// so a new optional field is readable on every document instead of only on the ones written
// after the change. A required field is refused: an ALTER has no value to supply for rows
// that already exist, which is why Postgres insists on a DEFAULT for NOT NULL. Paths are
// kept top level because nesting a missing object would invent data no one asked for.
func (e *Engine) expandAlter(changes []Change, now time.Time) ([]Change, error) {
	extra := []Change{}
	for _, c := range changes {
		if c.Record.Kind != "collection" || c.Expected < 1 || c.Record.Deleted {
			continue
		}
		h := e.records[c.Record.ID]
		if len(h) == 0 {
			continue
		}
		old := h[len(h)-1]
		if old.Deleted || old.Workspace != c.Record.Workspace {
			continue
		}
		before, err := schemaOf(old)
		if err != nil {
			return nil, err
		}
		after, err := schemaOf(c.Record)
		if err != nil {
			return nil, err
		}
		added := []string{}
		for path, field := range after.Fields {
			if _, had := before.Fields[path]; had {
				continue
			}
			if strings.Contains(path, ".") {
				return nil, fmt.Errorf("an added field must be top level: %s", path)
			}
			if field.Required {
				return nil, fmt.Errorf("required field %s cannot be added to a collection that has rows", path)
			}
			added = append(added, path)
		}
		if len(added) == 0 {
			continue
		}
		sort.Strings(added)
		for _, docHistory := range e.records {
			r := docHistory[len(docHistory)-1]
			if r.Kind != "document" || r.Collection != old.Collection || r.Workspace != old.Workspace ||
				!active(r, now) {
				continue
			}
			rec := clone(r)
			if rec.Data == nil {
				rec.Data = map[string]any{}
			}
			filled := false
			for _, path := range added {
				if _, present := rec.Data[path]; !present {
					rec.Data[path] = zeroValue(after.Fields[path].Type)
					filled = true
				}
			}
			if filled {
				extra = append(extra, Change{Record: rec, Expected: r.Version})
			}
		}
	}
	if len(extra) == 0 {
		return changes, nil
	}
	if len(changes)+len(extra) > 256 {
		return nil, errors.New("schema change touches too many rows for one transaction")
	}
	joined := make([]Change, 0, len(changes)+len(extra))
	joined = append(joined, changes...)
	return append(joined, extra...), nil
}

func (e *Engine) Put(r Record, expected int) (Record, error) {
	rs, err := e.Batch([]Change{{r, expected}}, "")
	if err != nil {
		return Record{}, err
	}
	return rs[0], nil
}

// Mutate re-reads the live record and applies set, unset and inc in one locked
// transaction, so a counter cannot lose a concurrent write the way a client-side
// read-then-versioned-PATCH does. inc adds to a field a record is missing as if it
// were zero, and refuses to add onto text.
func (e *Engine) Mutate(id string, set map[string]any, unset []string, inc map[string]any) (Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := e.records[id]
	if len(h) == 0 {
		return Record{}, ErrMissing
	}
	old := h[len(h)-1]
	if old.Deleted {
		return Record{}, ErrMissing
	}
	rec := clone(old)
	if rec.Data == nil {
		rec.Data = map[string]any{}
	}
	for _, field := range unset {
		delete(rec.Data, field)
	}
	for field, delta := range inc {
		step, ok := number(delta)
		if !ok {
			return Record{}, fmt.Errorf("increment on %s must be a number", field)
		}
		current, exists := rec.Data[field]
		if exists {
			base, numeric := number(current)
			if !numeric {
				return Record{}, fmt.Errorf("cannot increment non-numeric field %s", field)
			}
			step = new(big.Rat).Add(base, step)
		}
		rec.Data[field] = rationalJSON(step)
	}
	for k, v := range set {
		rec.Data[k] = v
	}
	rs, err := e.batchLocked([]Change{{rec, old.Version}}, "")
	if err != nil {
		return Record{}, err
	}
	return rs[0], nil
}
func (e *Engine) Batch(changes []Change, key string) ([]Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.batchLocked(changes, key)
}
func (e *Engine) batchLocked(changes []Change, key string) ([]Record, error) {
	if e.closed || e.poisoned {
		return nil, ErrStorage
	}
	if len(changes) == 0 || len(changes) > 256 {
		return nil, errors.New("transaction requires 1..256 changes")
	}
	if len(key) > 256 {
		return nil, errors.New("idempotency key too long")
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	if key != "" {
		if receipt, ok := e.receipts[key]; ok {
			if receipt.Hash != hash {
				return nil, ErrConflict
			}
			return cloneRecords(receipt.Records), nil
		}
	}
	candidate := make(map[string][]Record, len(e.records))
	for k, v := range e.records {
		candidate[k] = v
	}
	now := time.Now().UTC()
	if changes, err = e.expandAlter(changes, now); err != nil {
		return nil, err
	}
	prepared := make([]Record, 0, len(changes))
	touched := map[string]bool{}
	for _, change := range changes {
		r := clone(change.Record)
		if err = valid(r); err != nil {
			return nil, err
		}
		if r.ID == "" {
			if change.Expected != 0 {
				return nil, ErrConflict
			}
			r.ID, err = id()
			if err != nil {
				return nil, err
			}
		}
		if touched[r.ID] {
			return nil, errors.New("one change per record per transaction")
		}
		touched[r.ID] = true
		h := candidate[r.ID]
		version := 0
		if len(h) > 0 {
			old := h[len(h)-1]
			version = old.Version
			if old.Deleted {
				return nil, ErrMissing
			}
			if old.User != r.User || old.Workspace != r.Workspace || old.Collection != r.Collection || old.Kind != r.Kind {
				return nil, errors.New("owner, workspace, kind and collection are immutable")
			}
		}
		if version != change.Expected {
			return nil, ErrConflict
		}
		r.Version = version + 1
		r.Created = now
		nh := append([]Record{}, h...)
		candidate[r.ID] = append(nh, r)
		prepared = append(prepared, r)
	}
	for _, r := range prepared {
		if r.Deleted {
			continue
		}
		for _, target := range append(append([]string{}, r.Links...), r.Parent) {
			if target == "" {
				continue
			}
			h := candidate[target]
			if len(h) == 0 {
				return nil, errors.New("link target missing")
			}
			other := h[len(h)-1]
			if !active(other, now) || !visible(other, r.User, r.Workspace) {
				return nil, errors.New("link target outside readable scope")
			}
		}
	}
	if err = validateCollections(candidate, now); err != nil {
		return nil, err
	}
	event := Event{Format: 2, Records: prepared, Key: key, Hash: hash, Sequence: e.sequence + 1}
	if err = e.appendEvent(event); err != nil {
		return nil, err
	}
	e.records = candidate
	e.rebuildIndexes()
	e.sequence = event.Sequence
	if key != "" {
		e.receipts[key] = Receipt{hash, cloneRecords(prepared)}
	}
	return cloneRecords(prepared), nil
}
func frame(event Event) ([]byte, error) {
	b, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(b) > maxFrame {
		return nil, errors.New("transaction/snapshot exceeds 32 MiB")
	}
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	out = append(out, b...)
	sum := sha256.Sum256(b)
	return append(out, sum[:]...), nil
}
func (e *Engine) appendEvent(event Event) error {
	b, err := frame(event)
	if err != nil {
		return err
	}
	n, err := e.writeFrame(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = e.syncFile()
	}
	if err != nil {
		e.poisoned = true
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return nil
}
func (e *Engine) History(id string) []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneRecords(e.records[id])
}
func (e *Engine) Latest(id string) (Record, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	h := e.records[id]
	if len(h) == 0 || !active(h[len(h)-1], time.Now()) {
		return Record{}, ErrMissing
	}
	return clone(h[len(h)-1]), nil
}
func (e *Engine) Sequence() uint64 { e.mu.RLock(); defer e.mu.RUnlock(); return e.sequence }
func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) })
}
func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var ma, mb float64
	for i, v := range a {
		ma = math.Max(ma, math.Abs(v))
		mb = math.Max(mb, math.Abs(b[i]))
	}
	if ma == 0 || mb == 0 {
		return 0
	}
	var dot, na, nb float64
	for i, v := range a {
		x, y := v/ma, b[i]/mb
		dot += x * y
		na += x * x
		nb += y * y
	}
	return dot / math.Sqrt(na*nb)
}

type Query struct {
	User, Workspace, Kind, Source, Text, EmbeddingModel, Agent string
	Vector                                                     []float64
	Limit                                                      int
}
type Hit struct {
	Record Record  `json:"record"`
	Score  float64 `json:"score"`
}

// Search scans the authorized partition. BM25 and exact cosine are fused by RRF.
// There is no ANN index or learned reranker in this prototype.
func (e *Engine) Search(q Query) []Hit {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := []Hit{}
	now := time.Now()
	docs := []Record{}
	terms := [][]string{}
	df := map[string]int{}
	total := 0
	for _, h := range e.records {
		r := h[len(h)-1]
		if r.Metadata["status"] == "superseded" || (q.Kind == "" && (r.Kind == "document" || r.Kind == "collection" || r.Kind == "job")) {
			continue
		}
		if r.Deleted || (r.Expires != nil && !r.Expires.After(now)) || !visible(r, q.User, q.Workspace) || (q.Agent != "" && len(r.Agents) > 0 && !contains(r.Agents, q.Agent)) || (q.Kind != "" && q.Kind != r.Kind) || (q.Source != "" && q.Source != r.Source) {
			continue
		}
		docs = append(docs, r)
		ts := tokens(r.Content)
		terms = append(terms, ts)
		total += len(ts)
		seen := map[string]bool{}
		for _, t := range ts {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	n := len(docs)
	if n == 0 {
		return out
	}
	avg := float64(total) / float64(n)
	if avg == 0 {
		avg = 1
	}
	qt := tokens(q.Text)
	lexical := []Hit{}
	semantic := []Hit{}
	for i, r := range docs {
		if q.Text == "" && len(q.Vector) == 0 {
			out = append(out, Hit{clone(r), 0})
			continue
		}
		counts := map[string]int{}
		for _, t := range terms[i] {
			counts[t]++
		}
		score := 0.0
		for _, t := range qt {
			tf := float64(counts[t])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(n-df[t])+0.5)/(float64(df[t])+0.5))
			score += idf * (tf * 2.2) / (tf + 1.2*(0.25+0.75*float64(len(terms[i]))/avg))
		}
		if score > 0 {
			lexical = append(lexical, Hit{r, score})
		}
		c := cosine(q.Vector, r.Vector)
		if q.EmbeddingModel != r.EmbeddingModel {
			c = 0
		}
		if c > 0 {
			semantic = append(semantic, Hit{r, c})
		}
	}
	order := func(h []Hit) {
		sort.Slice(h, func(i, j int) bool {
			if h[i].Score == h[j].Score {
				return h[i].Record.ID < h[j].Record.ID
			}
			return h[i].Score > h[j].Score
		})
	}
	order(lexical)
	order(semantic)
	scores := map[string]float64{}
	records := map[string]Record{}
	for _, list := range [][]Hit{lexical, semantic} {
		for rank, h := range list {
			scores[h.Record.ID] += 1 / float64(61+rank)
			records[h.Record.ID] = h.Record
		}
	}
	for id, score := range scores {
		out = append(out, Hit{clone(records[id]), score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].Record.Created.Equal(out[j].Record.Created) {
				return out[i].Record.ID < out[j].Record.ID
			}
			return out[i].Record.Created.After(out[j].Record.Created)
		}
		return out[i].Score > out[j].Score
	})
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Operational surface: online backups, Prometheus metrics, per-principal rate limiting and
// physical purge. These act on the whole server rather than one workspace, so every route
// here needs an admin token. In a deployment where workspace admins are different parties,
// give only the operator an admin principal in a workspace of its own.

// opsState is shared by every per-request copy of Server. A nil value (as in tests that
// build a Server literal) turns metrics, rate limiting and backups off.
type opsState struct {
	backupDir string
	limiter   *limiter
	metrics   *metrics
	started   time.Time
}

// ---- rate limiting ----

// limiter is a token bucket per principal: rate requests per second on average, up to burst
// at once. Buckets exist only for authenticated principals, so their number is bounded by the
// configured identities, not by callers.
type limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]*bucketState
	now     func() time.Time
}
type bucketState struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate, burst float64) *limiter {
	if burst < 1 {
		burst = math.Max(1, 2*rate)
	}
	return &limiter{rate: rate, burst: burst, buckets: map[string]*bucketState{}, now: time.Now}
}

// allow takes one token for key, or reports how long until one is available.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucketState{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// ---- metrics ----

var latencyBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type histogram struct {
	counts []uint64 // one per bucket, cumulative at render time
	sum    float64
	count  uint64
}

type metrics struct {
	mu          sync.Mutex
	requests    map[string]uint64 // route + "\x00" + status code
	latency     map[string]*histogram
	rateLimited uint64
}

func newMetrics() *metrics {
	return &metrics{requests: map[string]uint64{}, latency: map[string]*histogram{}}
}

// routeLabel keeps the label set small and free of ids: the first path segment under
// /api/v1/, or a fixed name for everything else.
func routeLabel(path string) string {
	switch {
	case path == "/":
		return "console"
	case path == "/studio/":
		return "studio"
	case path == "/metrics":
		return "metrics"
	case strings.HasPrefix(path, "/api/v1/"):
		rest := strings.TrimPrefix(path, "/api/v1/")
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		switch rest {
		case "health", "collections", "documents", "query", "transactions", "memories", "search", "ingest",
			"jobs", "sources", "demo", "wiki", "graph", "code", "skills", "admin":
			return rest
		}
	}
	return "other"
}

func (m *metrics) observe(route string, status int, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[route+"\x00"+strconv.Itoa(status)]++
	h := m.latency[route]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(latencyBuckets))}
		m.latency[route] = h
	}
	s := d.Seconds()
	for i, le := range latencyBuckets {
		if s <= le {
			h.counts[i]++
			break
		}
	}
	h.sum += s
	h.count++
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}
func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	return r.ResponseWriter.Write(b)
}

// EngineStats is a point-in-time view of the engine for monitoring.
type EngineStats struct {
	Sequence     uint64
	JournalBytes int64
	Poisoned     bool
	Records      map[string]int // live (latest, not deleted) records by kind
	Revisions    int
	Receipts     int
}

func (e *Engine) Stats() EngineStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := EngineStats{Sequence: e.sequence, Poisoned: e.poisoned, Records: map[string]int{}, Receipts: len(e.receipts)}
	if e.file != nil {
		if fi, err := e.file.Stat(); err == nil {
			st.JournalBytes = fi.Size()
		}
	}
	for _, h := range e.records {
		st.Revisions += len(h)
		if r := h[len(h)-1]; !r.Deleted {
			st.Records[r.Kind]++
		}
	}
	return st
}

func promEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

// render writes the Prometheus text exposition format, version 0.0.4.
func (o *opsState) render(e *Engine) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	st := e.Stats()
	line("# HELP memorydb_build_info Version of the running server.")
	line("# TYPE memorydb_build_info gauge")
	line(`memorydb_build_info{version="%s"} 1`, promEscape(version))
	line("# HELP memorydb_uptime_seconds Seconds since the server started.")
	line("# TYPE memorydb_uptime_seconds gauge")
	line("memorydb_uptime_seconds %.3f", time.Since(o.started).Seconds())
	line("# HELP memorydb_journal_sequence Last committed transaction sequence.")
	line("# TYPE memorydb_journal_sequence gauge")
	line("memorydb_journal_sequence %d", st.Sequence)
	line("# HELP memorydb_journal_bytes Size of the journal file.")
	line("# TYPE memorydb_journal_bytes gauge")
	line("memorydb_journal_bytes %d", st.JournalBytes)
	line("# HELP memorydb_storage_poisoned 1 when writes are refused after an I/O failure; restart required.")
	line("# TYPE memorydb_storage_poisoned gauge")
	poisoned := 0
	if st.Poisoned {
		poisoned = 1
	}
	line("memorydb_storage_poisoned %d", poisoned)
	line("# HELP memorydb_records Live records (latest version not deleted) by kind.")
	line("# TYPE memorydb_records gauge")
	kinds := make([]string, 0, len(st.Records))
	for k := range st.Records {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		line(`memorydb_records{kind="%s"} %d`, promEscape(k), st.Records[k])
	}
	line("# HELP memorydb_revisions Stored revisions across all records, tombstones included.")
	line("# TYPE memorydb_revisions gauge")
	line("memorydb_revisions %d", st.Revisions)
	line("# HELP memorydb_idempotency_receipts Stored idempotency receipts.")
	line("# TYPE memorydb_idempotency_receipts gauge")
	line("memorydb_idempotency_receipts %d", st.Receipts)
	if o.metrics == nil {
		return b.String()
	}
	m := o.metrics
	m.mu.Lock()
	defer m.mu.Unlock()
	line("# HELP memorydb_rate_limited_total Requests refused with 429 by the per-principal limiter.")
	line("# TYPE memorydb_rate_limited_total counter")
	line("memorydb_rate_limited_total %d", m.rateLimited)
	line("# HELP memorydb_http_requests_total HTTP requests by route and status code.")
	line("# TYPE memorydb_http_requests_total counter")
	keys := make([]string, 0, len(m.requests))
	for k := range m.requests {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.SplitN(k, "\x00", 2)
		line(`memorydb_http_requests_total{route="%s",code="%s"} %d`, parts[0], parts[1], m.requests[k])
	}
	line("# HELP memorydb_http_request_duration_seconds HTTP request latency by route.")
	line("# TYPE memorydb_http_request_duration_seconds histogram")
	routes := make([]string, 0, len(m.latency))
	for r := range m.latency {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	for _, r := range routes {
		h := m.latency[r]
		var cum uint64
		for i, le := range latencyBuckets {
			cum += h.counts[i]
			line(`memorydb_http_request_duration_seconds_bucket{route="%s",le="%s"} %d`, r, strconv.FormatFloat(le, 'g', -1, 64), cum)
		}
		line(`memorydb_http_request_duration_seconds_bucket{route="%s",le="+Inf"} %d`, r, h.count)
		line(`memorydb_http_request_duration_seconds_sum{route="%s"} %g`, r, h.sum)
		line(`memorydb_http_request_duration_seconds_count{route="%s"} %d`, r, h.count)
	}
	return b.String()
}

// ---- online backup ----

// BackupResult describes a backup written while the server kept serving.
type BackupResult struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Sequence uint64 `json:"sequence"`
}

// onlineBackup writes a checksummed copy of the journal into dir under a name that carries
// the time and sequence, so a cron job can call it repeatedly without choosing names. Writers
// wait while the committed prefix is copied; readers do not.
func (e *Engine) onlineBackup(dir string) (BackupResult, error) {
	seq := e.Sequence()
	name := fmt.Sprintf("memorydb-%s-seq%d.bak", time.Now().UTC().Format("20060102T150405.000Z"), seq)
	dst := filepath.Join(dir, name)
	if err := e.Backup(dst); err != nil {
		return BackupResult{}, err
	}
	sum, err := os.ReadFile(dst + ".sha256")
	if err != nil {
		return BackupResult{}, err
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return BackupResult{}, err
	}
	return BackupResult{Path: dst, Bytes: fi.Size(), SHA256: strings.TrimSpace(string(sum)), Sequence: seq}, nil
}

// ---- purge ----

// Purge erases every stored revision of already-deleted records, and their copies inside
// idempotency receipts, by rewriting the journal as a snapshot without them. A record must be
// deleted first, through the normal permission checks, and belong to workspace. Purge cannot
// reach backups taken earlier, and the filesystem may keep the replaced journal's blocks until
// they are reused; it removes the data from this database, not from the disk's history.
func (e *Engine) Purge(workspace string, ids []string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.poisoned {
		return 0, ErrStorage
	}
	if len(ids) == 0 || len(ids) > 1000 {
		return 0, errors.New("purge requires 1..1000 ids")
	}
	purge := map[string]bool{}
	for _, id := range ids {
		h := e.records[id]
		if len(h) == 0 {
			return 0, fmt.Errorf("%w: %s", ErrMissing, id)
		}
		last := h[len(h)-1]
		if last.Workspace != workspace {
			return 0, fmt.Errorf("%w: %s", ErrMissing, id)
		}
		if !last.Deleted {
			return 0, fmt.Errorf("record %s must be deleted before it can be purged", id)
		}
		purge[id] = true
	}
	next := make(map[string][]Record, len(e.records))
	for id, h := range e.records {
		if !purge[id] {
			next[id] = h
		}
	}
	receipts := make(map[string]Receipt, len(e.receipts))
	for k, rc := range e.receipts {
		kept := make([]Record, 0, len(rc.Records))
		for _, r := range rc.Records {
			if !purge[r.ID] {
				kept = append(kept, r)
			}
		}
		if len(kept) != len(rc.Records) {
			rc = Receipt{Hash: rc.Hash, Records: kept, Purged: rc.Purged + len(rc.Records) - len(kept)}
		}
		receipts[k] = rc
	}
	if err := e.rewriteLocked(next, receipts); err != nil {
		return 0, err
	}
	return len(purge), nil
}

// ---- routes ----

func (s *Server) opsRoutes(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/metrics" && !strings.HasPrefix(path, "/api/v1/admin/") {
		return false
	}
	if s.role != "admin" {
		failure(w, 403, errors.New("admin required"))
		return true
	}
	switch {
	case path == "/metrics" && r.Method == "GET":
		o := s.ops
		if o == nil {
			o = &opsState{started: time.Now()}
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte(o.render(s.engine)))
	case path == "/api/v1/admin/backup/" && r.Method == "POST":
		if s.ops == nil || s.ops.backupDir == "" {
			failure(w, 404, errors.New("online backups are off; start the server with -backup-dir"))
			return true
		}
		res, err := s.engine.onlineBackup(s.ops.backupDir)
		if err != nil {
			fail(w, err)
			return true
		}
		respond(w, 201, res)
	case path == "/api/v1/admin/purge/" && r.Method == "POST":
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return true
		}
		n, err := s.engine.Purge(s.workspace, in.IDs)
		if err != nil {
			fail(w, err)
			return true
		}
		respond(w, 200, map[string]any{"purged": n, "sequence": s.engine.Sequence()})
	default:
		failure(w, 404, errors.New("route or method not found"))
	}
	return true
}

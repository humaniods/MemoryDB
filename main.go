package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed ui.html
var ui []byte

//go:embed console.html
var consoleUI []byte

type Principal struct {
	TokenHash string `json:"token_sha256"`
	User      string `json:"user"`
	Workspace string `json:"workspace"`
	Role      string `json:"role"`
}
type Server struct {
	engine     *Engine
	token      string
	user       string
	workspace  string
	role       string
	principals []Principal
}

func respond(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "response encoding failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}
func failure(w http.ResponseWriter, status int, err error) {
	respond(w, status, map[string]string{"error": err.Error()})
}
func fail(w http.ResponseWriter, err error) {
	status := 400
	switch {
	case errors.Is(err, ErrStorage):
		status = 503
	case errors.Is(err, ErrConflict):
		status = 409
	case errors.Is(err, ErrMissing):
		status = 404
	}
	failure(w, status, err)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	d := json.NewDecoder(r.Body)
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func (s *Server) authenticate(r *http.Request) (Principal, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return Principal{}, false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	h := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(h[:])
	for _, p := range s.principals {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(p.TokenHash)) == 1 {
			return p, true
		}
	}
	if s.token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1 {
		return Principal{User: s.user, Workspace: s.workspace, Role: "admin"}, true
	}
	return Principal{}, false
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if (r.URL.Path == "/" || r.URL.Path == "/studio/") && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/" {
			w.Write(consoleUI)
		} else {
			w.Write(ui)
		}
		return
	}
	p, ok := s.authenticate(r)
	if !ok {
		failure(w, 401, errors.New("valid bearer token required"))
		return
	}
	scoped := *s
	scoped.user = p.User
	scoped.workspace = p.Workspace
	scoped.role = p.Role
	if p.Role == "reader" && r.Method != "GET" && !(r.Method == "POST" && (strings.HasPrefix(r.URL.Path, "/api/v1/query/") || r.URL.Path == "/api/v1/search/" || r.URL.Path == "/api/v1/graph/")) {
		failure(w, 403, errors.New("read-only principal"))
		return
	}
	scoped.route(w, r)
}
func (s *Server) key(r *http.Request) string {
	k := r.Header.Get("Idempotency-Key")
	if k == "" {
		return ""
	}
	return s.workspace + ":" + s.user + ":" + r.URL.Path + ":" + k
}
func (s *Server) readable(id string) (Record, error) {
	rec, err := s.engine.Latest(id)
	if err != nil || !visible(rec, s.user, s.workspace) {
		return Record{}, ErrMissing
	}
	return rec, nil
}
func (s *Server) canWrite(rec Record) bool {
	return rec.Workspace == s.workspace && (rec.User == s.user || (s.role == "admin" && rec.Visibility == "team"))
}
func (s *Server) prepare(rec Record, expected int) (Record, error) {
	if expected < 0 {
		return Record{}, ErrConflict
	}
	if expected == 0 {
		if rec.ID != "" || rec.Version != 0 || rec.Deleted {
			return Record{}, errors.New("create must not set id, version or deleted")
		}
		rec.User = s.user
		rec.Workspace = s.workspace
		if rec.Kind == "skill" {
			if rec.Metadata == nil {
				rec.Metadata = map[string]string{}
			}
			rec.Metadata["status"] = "draft"
			delete(rec.Metadata, "approved_by")
		}
		return rec, nil
	}
	old, err := s.readable(rec.ID)
	if err != nil {
		return Record{}, err
	}
	if !s.canWrite(old) {
		return Record{}, errors.New("record owner required")
	}
	rec.User = old.User
	rec.Workspace = old.Workspace
	if rec.Kind == "skill" {
		if rec.Metadata == nil {
			rec.Metadata = map[string]string{}
		}
		rec.Metadata["status"] = "draft"
		delete(rec.Metadata, "approved_by")
	}
	return rec, nil
}
func (s *Server) commit(w http.ResponseWriter, r *http.Request, rec Record, expected, status int) {
	prepared, err := s.prepare(rec, expected)
	if err != nil {
		fail(w, err)
		return
	}
	rs, err := s.engine.Batch([]Change{{prepared, expected}}, s.key(r))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, status, rs[0])
}
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/v1/health/" && r.Method == "GET" {
		respond(w, 200, map[string]any{"status": "ok", "workspace": s.workspace, "user": s.user, "role": s.role, "version": "0.2.0", "sequence": s.engine.Sequence()})
		return
	}
	if path == "/api/v1/collections/" {
		if r.Method == "GET" {
			respond(w, 200, map[string]any{"results": s.engine.Collections(s.workspace)})
			return
		}
		if r.Method == "POST" {
			if s.role != "admin" {
				failure(w, 403, errors.New("admin required"))
				return
			}
			var input struct {
				Name   string `json:"name"`
				Schema Schema `json:"schema"`
			}
			if err := decode(w, r, &input); err != nil {
				fail(w, err)
				return
			}
			b, _ := json.Marshal(input.Schema)
			var data map[string]any
			decodeJSON(b, &data)
			s.commit(w, r, Record{Kind: "collection", Collection: input.Name, Content: input.Name, Data: data, Visibility: "team"}, 0, 201)
			return
		}
	}
	if strings.HasPrefix(path, "/api/v1/query/") && r.Method == "POST" {
		col := strings.Trim(strings.TrimPrefix(path, "/api/v1/query/"), "/")
		if !validName(col) {
			fail(w, errors.New("invalid collection"))
			return
		}
		var q DocumentQuery
		if err := decode(w, r, &q); err != nil {
			fail(w, err)
			return
		}
		page, err := s.engine.Documents(s.user, s.workspace, col, q)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, page)
		return
	}
	if path == "/api/v1/transactions/" && r.Method == "POST" {
		var req struct {
			Changes []Change `json:"changes"`
		}
		if err := decode(w, r, &req); err != nil {
			fail(w, err)
			return
		}
		for i, c := range req.Changes {
			if c.Record.Kind == "collection" && s.role != "admin" {
				failure(w, 403, errors.New("admin required for schema changes"))
				return
			}
			p, err := s.prepare(c.Record, c.Expected)
			if err != nil {
				fail(w, err)
				return
			}
			req.Changes[i].Record = p
		}
		rs, err := s.engine.Batch(req.Changes, s.key(r))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]any{"results": rs})
		return
	}
	if strings.HasPrefix(path, "/api/v1/documents/") {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1/documents/"), "/"), "/")
		if len(parts) < 1 || len(parts) > 2 || !validName(parts[0]) {
			fail(w, errors.New("invalid document path"))
			return
		}
		col := parts[0]
		if len(parts) == 1 {
			if r.Method == "GET" {
				page, err := s.engine.Documents(s.user, s.workspace, col, DocumentQuery{})
				if err != nil {
					fail(w, err)
				} else {
					respond(w, 200, page)
				}
				return
			}
			if r.Method == "POST" {
				var rec Record
				if err := decode(w, r, &rec); err != nil {
					fail(w, err)
					return
				}
				rec.Kind = "document"
				rec.Collection = col
				if rec.Visibility == "" {
					rec.Visibility = "team"
				}
				s.commit(w, r, rec, 0, 201)
				return
			}
		} else {
			rec, err := s.readable(parts[1])
			if err != nil || rec.Kind != "document" || rec.Collection != col {
				fail(w, ErrMissing)
				return
			}
			s.recordRoute(w, r, rec)
			return
		}
	}
	if path == "/api/v1/memories/" {
		switch r.Method {
		case "GET":
			q := r.URL.Query()
			limit, _ := strconv.Atoi(q.Get("limit"))
			respond(w, 200, map[string]any{"results": s.engine.Search(Query{User: s.user, Workspace: s.workspace, Kind: q.Get("kind"), Source: q.Get("source"), Text: q.Get("q"), Agent: q.Get("agent"), Limit: limit})})
			return
		case "POST":
			var rec Record
			if err := decode(w, r, &rec); err != nil {
				fail(w, err)
				return
			}
			if rec.Kind == "document" || rec.Kind == "collection" {
				fail(w, errors.New("use collections/documents endpoints"))
				return
			}
			s.commit(w, r, rec, 0, 201)
			return
		}
	}
	if path == "/api/v1/search/" && r.Method == "POST" {
		var q struct {
			Text   string    `json:"text"`
			Vector []float64 `json:"vector"`
			Kind   string    `json:"kind"`
			Source string    `json:"source"`
			Model  string    `json:"embedding_model"`
			Agent  string    `json:"agent"`
			Limit  int       `json:"limit"`
		}
		if err := decode(w, r, &q); err != nil {
			fail(w, err)
			return
		}
		if len(q.Vector) > 4096 {
			fail(w, errors.New("vector too large"))
			return
		}
		respond(w, 200, map[string]any{"results": s.engine.Search(Query{User: s.user, Workspace: s.workspace, Text: q.Text, Vector: q.Vector, Kind: q.Kind, Source: q.Source, EmbeddingModel: q.Model, Agent: q.Agent, Limit: q.Limit})})
		return
	}
	if strings.HasPrefix(path, "/api/v1/memories/") {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1/memories/"), "/"), "/")
		if len(parts) == 2 && parts[1] == "history" && r.Method == "GET" {
			h := s.engine.History(parts[0])
			if len(h) == 0 || !visible(h[len(h)-1], s.user, s.workspace) {
				fail(w, ErrMissing)
				return
			} // Only the owner can read prior private contents after sharing changes.
			if h[len(h)-1].User != s.user {
				failure(w, 403, errors.New("history requires owner"))
				return
			}
			respond(w, 200, map[string]any{"results": h})
			return
		}
		if len(parts) == 1 {
			rec, err := s.readable(parts[0])
			if err != nil {
				fail(w, err)
				return
			}
			if rec.Kind == "collection" && s.role != "admin" && r.Method != "GET" {
				failure(w, 403, errors.New("admin required"))
				return
			}
			s.recordRoute(w, r, rec)
			return
		}
	}
	if s.memoryRoutes(w, r) {
		return
	}
	failure(w, 404, errors.New("route or method not found"))
}
func (s *Server) recordRoute(w http.ResponseWriter, r *http.Request, old Record) {
	switch r.Method {
	case "GET":
		respond(w, 200, old)
	case "PUT":
		var rec Record
		if err := decode(w, r, &rec); err != nil {
			fail(w, err)
			return
		}
		if rec.Deleted {
			fail(w, errors.New("use DELETE"))
			return
		}
		rec.ID = old.ID
		rec.Kind = old.Kind
		rec.Collection = old.Collection
		s.commit(w, r, rec, rec.Version, 200)
	case "PATCH":
		var req struct {
			Version int            `json:"version"`
			Set     map[string]any `json:"set"`
			Unset   []string       `json:"unset"`
		}
		if err := decode(w, r, &req); err != nil {
			fail(w, err)
			return
		}
		if old.Kind != "document" {
			fail(w, errors.New("PATCH supports document data"))
			return
		}
		if old.Data == nil {
			old.Data = map[string]any{}
		}
		for k, v := range req.Set {
			old.Data[k] = v
		}
		for _, k := range req.Unset {
			delete(old.Data, k)
		}
		s.commit(w, r, old, req.Version, 200)
	case "DELETE":
		v, err := strconv.Atoi(r.Header.Get("If-Match"))
		if err != nil || v < 1 {
			fail(w, errors.New("If-Match revision required"))
			return
		}
		old.Deleted = true
		s.commit(w, r, old, v, 200)
	default:
		failure(w, 405, errors.New("method not allowed"))
	}
}
func loadPrincipals(path string) ([]Principal, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ps []Principal
	if err = json.Unmarshal(b, &ps); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, p := range ps {
		decoded, err := hex.DecodeString(p.TokenHash)
		if err != nil || len(decoded) != 32 || seen[p.TokenHash] || !validName(p.User) || !validName(p.Workspace) || (p.Role != "admin" && p.Role != "writer" && p.Role != "reader") {
			return nil, errors.New("invalid/duplicate principal configuration")
		}
		seen[p.TokenHash] = true
	}
	return ps, nil
}
func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	path := flag.String("data", "memory.wal", "journal file")
	auth := flag.String("auth", "", "principal JSON config")
	cert := flag.String("tls-cert", "", "TLS certificate")
	key := flag.String("tls-key", "", "TLS private key")
	insecure := flag.Bool("allow-insecure-network", false, "explicitly allow HTTP on non-loopback (trusted reverse proxy only)")
	restore := flag.String("restore", "", "restore a checksummed backup to a new -data path and exit")
	backup := flag.String("backup", "", "backup to a new path and exit")
	compact := flag.Int("compact", -1, "retain N revisions per record; 0=all; compact and exit")
	worker := flag.Bool("worker", false, "run durable ingestion jobs")
	verify := flag.Bool("verify", false, "verify journal and exit")
	flag.Parse()
	if *restore != "" {
		if err := Restore(*restore, *path); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Restore verified")
		return
	}
	e, err := Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer e.Close()
	if *backup != "" {
		if err = e.Backup(*backup); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Backup created")
		return
	}
	if *compact >= 0 {
		if err = e.Compact(*compact); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Compaction complete")
		return
	}
	if *verify {
		fmt.Printf("Verified transaction sequence %d\n", e.Sequence())
		return
	}
	ps, err := loadPrincipals(*auth)
	if err != nil {
		log.Fatal(err)
	}
	token := os.Getenv("MEMORYDB_TOKEN")
	if len(ps) == 0 && len(token) < 16 {
		log.Fatal("set MEMORYDB_TOKEN (16+ characters) or provide -auth config")
	}
	if token != "" && len(token) < 16 {
		log.Fatal("MEMORYDB_TOKEN too short")
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatal(err)
	}
	ip := net.ParseIP(host)
	tls := *cert != "" && *key != ""
	if (*cert == "") != (*key == "") {
		log.Fatal("both TLS certificate and key required")
	}
	if (ip == nil || !ip.IsLoopback()) && !tls && !*insecure {
		log.Fatal("non-loopback requires TLS or explicit trusted-proxy flag")
	}
	s := &Server{engine: e, token: token, user: "local-user", workspace: "default", role: "admin", principals: ps}
	server := &http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *worker {
		go s.worker(ctx)
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	fmt.Printf("MemoryDB 0.2.0 listening on %s\n", *addr)
	if tls {
		err = server.ListenAndServeTLS(*cert, *key)
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
	}
}

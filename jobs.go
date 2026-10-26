package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Jobs are durable records. A lease prevents duplicate runs and is reclaimable after a crash.
func (s *Server) jobRoutes(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/v1/jobs/" && r.Method == "POST" {
		var in Ingest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return true
		}
		b, _ := json.Marshal(in)
		var data map[string]any
		decodeJSON(b, &data)
		s.commit(w, r, Record{Kind: "job", Content: "ingest", Data: data, Metadata: map[string]string{"status": "pending", "attempts": "0"}}, 0, 201)
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/jobs/") && strings.HasSuffix(r.URL.Path, "/run/") && r.Method == "POST" {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/"), "/run/")
		rec, err := s.runJob(r.Context(), id)
		if err != nil {
			fail(w, err)
		} else {
			respond(w, 200, rec)
		}
		return true
	}
	return false
}
func (s *Server) runJob(ctx context.Context, id string) (Record, error) {
	rec, err := s.readable(id)
	if err != nil || rec.Kind != "job" || rec.User != s.user {
		return Record{}, ErrMissing
	}
	if rec.Metadata["status"] == "done" {
		return rec, nil
	}
	if rec.Metadata["status"] == "running" {
		until, _ := time.Parse(time.RFC3339Nano, rec.Metadata["lease_until"])
		if until.After(time.Now()) {
			return Record{}, ErrConflict
		}
	}
	if rec.Metadata == nil {
		rec.Metadata = map[string]string{}
	}
	attempts, _ := strconv.Atoi(rec.Metadata["attempts"])
	if attempts >= 5 {
		return Record{}, errors.New("job retry limit reached")
	}
	rec.Metadata["status"] = "running"
	rec.Metadata["attempts"] = strconv.Itoa(attempts + 1)
	rec.Metadata["lease_until"] = time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)
	claimed, err := s.engine.Put(rec, rec.Version)
	if err != nil {
		return Record{}, err
	}
	b, _ := json.Marshal(claimed.Data)
	var in Ingest
	if err = json.Unmarshal(b, &in); err == nil {
		_, err = s.ingest(ctx, in, "job:"+claimed.ID)
	}
	if err != nil {
		claimed.Metadata["status"] = "failed"
		claimed.Metadata["error"] = err.Error()
	} else {
		claimed.Metadata["status"] = "done"
		delete(claimed.Metadata, "error")
	}
	delete(claimed.Metadata, "lease_until")
	saved, writeErr := s.engine.Put(claimed, claimed.Version)
	if writeErr != nil {
		return Record{}, writeErr
	}
	return saved, err
}
func (s *Server) worker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C: // Read a bounded batch; each run still checks a versioned lease.
			s.engine.mu.RLock()
			jobs := []Record{}
			for _, h := range s.engine.records {
				r := h[len(h)-1]
				state := r.Metadata["status"]
				if r.Kind == "job" && active(r, time.Now()) && (state == "pending" || state == "running") {
					jobs = append(jobs, clone(r))
					if len(jobs) >= 20 {
						break
					}
				}
			}
			s.engine.mu.RUnlock()
			for _, job := range jobs {
				scoped := *s
				scoped.user = job.User
				scoped.workspace = job.Workspace
				scoped.role = "writer"
				_, _ = scoped.runJob(ctx, job.ID)
			}
		}
	}
}

package goclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientAuthVersionNumbersAndError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing token")
		}
		if r.Method == "DELETE" {
			if r.Header.Get("If-Match") != "3" {
				t.Error("missing version")
			}
			w.WriteHeader(409)
			w.Write([]byte(`{"error":"version conflict"}`))
			return
		}
		w.Write([]byte(`{"data":{"value":9007199254740993}}`))
	}))
	defer srv.Close()
	c := Client{URL: srv.URL, Token: "secret"}
	out, err := c.Get(context.Background(), "things", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if out["data"].(map[string]any)["value"].(json.Number).String() != "9007199254740993" {
		t.Fatal("precision lost")
	}
	err = c.Delete(context.Background(), "things", "abc", 3)
	e, ok := err.(*Error)
	if !ok || e.Status != 409 {
		t.Fatal(err)
	}
}

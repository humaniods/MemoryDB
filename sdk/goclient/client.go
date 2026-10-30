// Package goclient provides a dependency-free HTTP client for MemoryDB.
package goclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// Connect parses a memorydb://TOKEN@host:port (or memorydbs:// for TLS) connection
// string, the same form the Python and Node clients and the MEMORYDB_URL env var use.
func Connect(raw string) (*Client, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "memorydb" && u.Scheme != "memorydbs" {
		return nil, fmt.Errorf("connection string must start with memorydb:// or memorydbs://")
	}
	token := u.User.Username()
	if token == "" {
		return nil, fmt.Errorf("connection string must carry the token: memorydb://TOKEN@host:port")
	}
	host := u.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		port = "8080"
	}
	scheme := "http"
	if u.Scheme == "memorydbs" {
		scheme = "https"
	}
	return &Client{URL: scheme + "://" + host + ":" + port, Token: token}, nil
}

// ConnectEnv builds a client from the MEMORYDB_URL environment variable.
func ConnectEnv() (*Client, error) {
	raw := os.Getenv("MEMORYDB_URL")
	if raw == "" {
		return nil, fmt.Errorf("MEMORYDB_URL is not set")
	}
	return Connect(raw)
}
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("MemoryDB %d: %s", e.Status, e.Message) }
func (c *Client) Do(ctx context.Context, method, path string, body, out any, key string) error {
	return c.do(ctx, method, path, body, out, key, nil)
}
func (c *Client) do(ctx context.Context, method, path string, body, out any, key string, headers map[string]string) error {
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+"/api/v1/"+path, input)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	d := json.NewDecoder(io.LimitReader(resp.Body, 32<<20))
	d.UseNumber()
	if resp.StatusCode >= 400 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = d.Decode(&problem)
		return &Error{resp.StatusCode, problem.Error}
	}
	if out == nil {
		return nil
	}
	return d.Decode(out)
}
func (c *Client) Create(ctx context.Context, collection string, data any, key string) (map[string]any, error) {
	var out map[string]any
	err := c.Do(ctx, "POST", "documents/"+url.PathEscape(collection)+"/", map[string]any{"data": data}, &out, key)
	return out, err
}
func (c *Client) Query(ctx context.Context, collection string, query any) (map[string]any, error) {
	var out map[string]any
	err := c.Do(ctx, "POST", "query/"+url.PathEscape(collection)+"/", query, &out, "")
	return out, err
}

func (c *Client) Get(ctx context.Context, collection, id string) (map[string]any, error) {
	var out map[string]any
	err := c.Do(ctx, "GET", "documents/"+url.PathEscape(collection)+"/"+url.PathEscape(id)+"/", nil, &out, "")
	return out, err
}
func (c *Client) Update(ctx context.Context, collection, id string, version int, set map[string]any, unset []string) (map[string]any, error) {
	var out map[string]any
	err := c.Do(ctx, "PATCH", "documents/"+url.PathEscape(collection)+"/"+url.PathEscape(id)+"/", map[string]any{"version": version, "set": set, "unset": unset}, &out, "")
	return out, err
}
func (c *Client) Delete(ctx context.Context, collection, id string, version int) error {
	return c.do(ctx, "DELETE", "documents/"+url.PathEscape(collection)+"/"+url.PathEscape(id)+"/", nil, nil, "", map[string]string{"If-Match": strconv.Itoa(version)})
}

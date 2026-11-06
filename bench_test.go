package main

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

// These benchmarks measure the engine's CPU cost per operation as the corpus grows. fsync is
// replaced with a no-op so the numbers isolate what the engine itself does per write and per
// search; on real storage add the device's fsync latency to every write.

var benchWords = strings.Fields("renewal pricing invoice sla migration customer growth plan seats billing " +
	"support ticket escalation contract discount region rajasthan gujarat textiles solar onboarding " +
	"refund upgrade downgrade trial churn quarterly annual monthly usage quota api latency outage " +
	"integration webhook export import dashboard report owner account manager priority")

func benchSentence(rng *rand.Rand) string {
	n := 10 + rng.Intn(5)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = benchWords[rng.Intn(len(benchWords))]
	}
	return strings.Join(parts, " ")
}

func benchVector(rng *rand.Rand) []float64 {
	v := make([]float64, 64)
	for i := range v {
		v[i] = rng.Float64()*2 - 1
	}
	return v
}

func benchCollection() Record {
	return Record{Kind: "collection", Collection: "benchdocs", User: "u", Workspace: "w", Content: "benchdocs", Visibility: "team",
		Data: map[string]any{"fields": map[string]any{
			"n":    map[string]any{"type": "integer", "required": true},
			"tag":  map[string]any{"type": "string"},
			"code": map[string]any{"type": "string", "casefold": true},
		}, "unique": []any{"code"}, "indexes": []any{"tag"}}}
}

func benchDocument(i int, rng *rand.Rand) Record {
	return Record{Kind: "document", Collection: "benchdocs", User: "u", Workspace: "w",
		Data: map[string]any{"n": i, "tag": fmt.Sprintf("t%d", rng.Intn(100)), "code": fmt.Sprintf("C-%08d", i)}}
}

func benchMemory(rng *rand.Rand) Record {
	return Record{Kind: "fact", Content: benchSentence(rng), User: "u", Workspace: "w", Source: "bench",
		EmbeddingModel: "bench-64", Vector: benchVector(rng)}
}

// seedBench opens a throwaway engine holding n records: half memories with 64-dimension
// vectors and half documents in an indexed, unique-constrained collection.
func seedBench(b *testing.B, n int) (*Engine, *rand.Rand) {
	b.Helper()
	e, err := Open(filepath.Join(b.TempDir(), "bench.wal"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { e.Close() })
	e.syncFile = func() error { return nil }
	rng := rand.New(rand.NewSource(42))
	if _, err = e.Put(benchCollection(), 0); err != nil {
		b.Fatal(err)
	}
	changes := []Change{}
	flush := func() {
		if len(changes) == 0 {
			return
		}
		if _, err := e.Batch(changes, ""); err != nil {
			b.Fatal(err)
		}
		changes = changes[:0]
	}
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			changes = append(changes, Change{Record: benchMemory(rng)})
		} else {
			changes = append(changes, Change{Record: benchDocument(i, rng)})
		}
		if len(changes) == 256 {
			flush()
		}
	}
	flush()
	return e, rng
}

var benchSizes = []int{2000, 10000, 20000}

func BenchmarkPutMemory(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			e, rng := seedBench(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := e.Put(benchMemory(rng), 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPutUniqueDocument(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			e, rng := seedBench(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := e.Put(benchDocument(n+i, rng), 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSearchText(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			e, _ := seedBench(b, n)
			q := Query{User: "u", Workspace: "w", Text: "renewal pricing", Limit: 10}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if len(e.Search(q)) == 0 {
					b.Fatal("no hits")
				}
			}
		})
	}
}

func BenchmarkSearchHybrid(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			e, rng := seedBench(b, n)
			q := Query{User: "u", Workspace: "w", Text: "invoice sla migration", Vector: benchVector(rng), EmbeddingModel: "bench-64", Limit: 10}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if len(e.Search(q)) == 0 {
					b.Fatal("no hits")
				}
			}
		})
	}
}

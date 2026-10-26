package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GraphQuery struct {
	ID      string `json:"id"`
	Reverse bool   `json:"reverse"`
	Depth   int    `json:"depth"`
	Limit   int    `json:"limit"`
}

func (e *Engine) Graph(user, workspace string, q GraphQuery) ([]Record, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if q.Depth < 0 || q.Depth > 8 || q.Limit < 0 || q.Limit > 500 {
		return nil, errors.New("depth 0..8 and limit 0..500 required")
	}
	if q.Depth == 0 {
		q.Depth = 2
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	nodes := map[string]Record{}
	for id, h := range e.records {
		r := h[len(h)-1]
		if active(r, time.Now()) && visible(r, user, workspace) {
			nodes[id] = r
		}
	}
	if _, ok := nodes[q.ID]; !ok {
		return nil, ErrMissing
	}
	edges := map[string][]string{}
	for id, r := range nodes {
		for _, to := range r.Links {
			if _, ok := nodes[to]; ok {
				if q.Reverse {
					edges[to] = append(edges[to], id)
				} else {
					edges[id] = append(edges[id], to)
				}
			}
		}
	}
	for k := range edges {
		sort.Strings(edges[k])
	}
	seen := map[string]bool{q.ID: true}
	frontier := []string{q.ID}
	out := []Record{}
	for depth := 0; depth <= q.Depth && len(frontier) > 0; depth++ {
		next := []string{}
		for _, id := range frontier {
			out = append(out, clone(nodes[id]))
			if len(out) >= q.Limit {
				return out, nil
			}
			for _, to := range edges[id] {
				if !seen[to] {
					seen[to] = true
					next = append(next, to)
				}
			}
		}
		frontier = next
	}
	return out, nil
}

type CodeInput struct {
	Repository string            `json:"repository"`
	Branch     string            `json:"branch"`
	Commit     string            `json:"commit"`
	Files      map[string]string `json:"files"`
}

func (s *Server) indexCode(in CodeInput, key string) ([]Record, error) {
	if in.Repository == "" || in.Commit == "" || len(in.Files) == 0 || len(in.Files) > 50 {
		return nil, errors.New("repository, commit and 1..50 Go files required")
	}
	files := make([]string, 0, len(in.Files))
	for name := range in.Files {
		if !strings.HasSuffix(name, ".go") {
			return nil, errors.New("parser currently supports Go files only")
		}
		files = append(files, name)
	}
	sort.Strings(files)
	type symbol struct {
		Record  Record
		Decl    *ast.FuncDecl
		Scope   string
		Imports map[string]string
	}
	symbols := []symbol{}
	names := map[string]string{}
	fs := token.NewFileSet()
	for _, name := range files {
		source := in.Files[name]
		f, err := parser.ParseFile(fs, name, source, 0)
		if err != nil {
			return nil, err
		}
		imports := map[string]string{}
		for _, spec := range f.Imports {
			p, _ := strconv.Unquote(spec.Path.Value)
			alias := path.Base(p)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			imports[alias] = p
		}
		scope := path.Dir(name) + ":" + f.Name.Name
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			start, end := fs.Position(fn.Pos()).Offset, fs.Position(fn.End()).Offset
			symbolName := fn.Name.Name
			if fn.Recv != nil {
				symbolName = "method:" + symbolName + ":" + strconv.Itoa(start)
			}
			identity := s.workspace + "\x00" + s.user + "\x00" + in.Repository + "\x00" + in.Branch + "\x00" + in.Commit + "\x00" + name + "\x00" + symbolName
			sum := sha256.Sum256([]byte(identity))
			rid := hex.EncodeToString(sum[:16])
			r := Record{ID: rid, Kind: "code", User: s.user, Workspace: s.workspace, Content: source[start:end], Source: in.Repository, Metadata: map[string]string{"path": name, "symbol": symbolName, "package": f.Name.Name, "line": strconv.Itoa(fs.Position(fn.Pos()).Line), "branch": in.Branch, "commit": in.Commit, "graph": "static Go AST; method/dynamic targets unresolved"}}
			if fn.Recv == nil {
				symbolKey := scope + ":" + fn.Name.Name
				if _, exists := names[symbolKey]; exists {
					return nil, errors.New("duplicate function symbol in input package")
				}
				names[symbolKey] = rid
			}
			symbols = append(symbols, symbol{r, fn, scope, imports})
		}
	}
	if len(symbols) == 0 || len(symbols) > 256 {
		return nil, errors.New("index requires 1..256 function symbols")
	}
	changes := []Change{}
	for _, sym := range symbols {
		r := sym.Record
		unresolved := []string{}
		links := map[string]bool{}
		ast.Inspect(sym.Decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if target, ok := names[sym.Scope+":"+fn.Name]; ok && (fn.Obj == nil || fn.Obj.Kind == ast.Fun) {
					links[target] = true
				} else {
					unresolved = append(unresolved, fn.Name)
				}
			case *ast.SelectorExpr:
				if alias, ok := fn.X.(*ast.Ident); ok {
					if pkg, ok := sym.Imports[alias.Name]; ok {
						unresolved = append(unresolved, pkg+"."+fn.Sel.Name)
					} else {
						unresolved = append(unresolved, "dynamic:"+alias.Name+"."+fn.Sel.Name)
					}
				}
			}
			return true
		})
		for id := range links {
			r.Links = append(r.Links, id)
		}
		sort.Strings(r.Links)
		sort.Strings(unresolved)
		r.Metadata["unresolved_calls"] = strings.Join(unresolved, ", ")
		changes = append(changes, Change{r, 0})
	}
	// A commit identifier is immutable; repeated identical index requests use an explicit key.
	return s.engine.Batch(changes, key)
}

type RevisionInput struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Commit     string `json:"commit"`
	Parent     string `json:"parent,omitempty"`
	Path       string `json:"path"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Reason     string `json:"reason"`
}
type Patch struct {
	Start  int    `json:"start"`
	Delete int    `json:"delete"`
	Insert string `json:"insert"`
}

func diff(before, after string) Patch {
	a, b := []rune(before), []rune(after)
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	suffix := 0
	for suffix < len(a)-start && suffix < len(b)-start && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	return Patch{start, len(a) - start - suffix, string(b[start : len(b)-suffix])}
}
func applyDiff(before string, p Patch) (string, error) {
	rs := []rune(before)
	if p.Start < 0 || p.Delete < 0 || p.Start > len(rs) || p.Delete > len(rs)-p.Start {
		return "", errors.New("invalid patch")
	}
	return string(rs[:p.Start]) + p.Insert + string(rs[p.Start+p.Delete:]), nil
}
func hashText(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func (s *Server) revision(in RevisionInput, key string) (Record, error) {
	if in.Repository == "" || in.Branch == "" || in.Commit == "" || in.Path == "" {
		return Record{}, errors.New("repository, branch, commit and path required")
	}
	if in.Parent != "" {
		parent, err := s.readable(in.Parent)
		if err != nil || parent.Kind != "revision" || parent.Source != in.Repository || parent.Metadata["path"] != in.Path || parent.Metadata["after_hash"] != hashText(in.Before) {
			return Record{}, errors.New("revision parent/base mismatch")
		}
	}
	patch := diff(in.Before, in.After)
	rebuilt, err := applyDiff(in.Before, patch)
	if err != nil || rebuilt != in.After {
		return Record{}, errors.New("patch verification failed")
	}
	data := map[string]any{"before": in.Before, "after": in.After, "patch": map[string]any{"start": patch.Start, "delete": patch.Delete, "insert": patch.Insert}}
	r := Record{Kind: "revision", Content: in.Reason, User: s.user, Workspace: s.workspace, Source: in.Repository, Parent: in.Parent, Data: data, Metadata: map[string]string{"path": in.Path, "branch": in.Branch, "commit": in.Commit, "before_hash": hashText(in.Before), "after_hash": hashText(in.After)}}
	if r.Content == "" {
		r.Content = "Revision " + in.Commit
	}
	rs, err := s.engine.Batch([]Change{{r, 0}}, key)
	if err != nil {
		return Record{}, err
	}
	return rs[0], nil
}

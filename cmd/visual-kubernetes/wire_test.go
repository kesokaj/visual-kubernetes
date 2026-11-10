package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

// modelConsts returns the string values of every constant of the named type in the model
// package, read from source so a constant added there is seen here without editing the test.
func modelConsts(t *testing.T, typ string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "internal", "model", "model.go"), nil, 0)
	if err != nil {
		t.Fatalf("parsing model.go: %v", err)
	}
	var out []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != typ {
				continue
			}
			for _, v := range vs.Values {
				if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					out = append(out, s)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// wireList returns the quoted strings of `export const <name> = [ ... ]` in web/src/lib/wire.ts.
func wireList(t *testing.T, src []byte, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)export const ` + name + ` = \[(.*?)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("wire.ts has no `export const %s = [...]`", name)
	}
	var out []string
	for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllSubmatch(m[1], -1) {
		out = append(out, string(q[1]))
	}
	slices.Sort(out)
	return out
}

// TestWireListsMatchModel pins the page's copies of the filter groups and edge kinds to the Go
// model in both directions: a value the page does not list is an item it drops (review phase 3).
func TestWireListsMatchModel(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "wire.ts"))
	if err != nil {
		t.Fatalf("reading wire.ts: %v", err)
	}
	for _, c := range []struct{ goType, tsList string }{
		{"FilterGroup", "FILTER_GROUPS"},
		{"EdgeKind", "EDGE_KINDS"},
	} {
		want := modelConsts(t, c.goType)
		if len(want) == 0 {
			t.Fatalf("found no %s constants in model.go: the reader is blind", c.goType)
		}
		if got := wireList(t, src, c.tsList); !slices.Equal(got, want) {
			t.Errorf("wire.ts %s = %v, model %s = %v", c.tsList, got, c.goType, want)
		}
	}
}

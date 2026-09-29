package repair

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestLossHelpersAreLinted keeps .golangci.yml's unusedresult list in step with the
// code: every function in this package that RETURNS the values a merge chose away
// (a mergedFacts, in any shape) must be listed there, so a call that discards that
// return fails the linter instead of deleting a fact with nothing in the notes to
// say so. A helper added or renamed without the list edit is caught here, and a
// listed name that no longer exists is caught too (it would guard nothing).
//
// The analyzer reaches package-level functions only, so a METHOD returning losses
// is refused outright: it could never be linted.
func TestLossHelpersAreLinted(t *testing.T) {
	const pkgPath = "github.com/kodestar/audiosilo-meta/internal/repair"

	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var helpers []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil || !mentionsIdent(fn.Type.Results, "mergedFacts") {
				continue
			}
			if fn.Recv != nil {
				t.Errorf("%s: method %s returns mergedFacts, which unusedresult cannot lint - make it a function", path, fn.Name.Name)
				continue
			}
			helpers = append(helpers, fn.Name.Name)
		}
	}
	if len(helpers) == 0 {
		t.Fatal("found no loss-returning helper: the scan is broken")
	}

	cfg, err := os.ReadFile(filepath.Join("..", "..", ".golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	re := regexp.MustCompile(`(?m)^\s*-\s*` + regexp.QuoteMeta(pkgPath) + `\.(\w+)\s*$`)
	for _, m := range re.FindAllStringSubmatch(string(cfg), -1) {
		listed[m[1]] = true
	}

	sort.Strings(helpers)
	for _, name := range helpers {
		if !listed[name] {
			t.Errorf(".golangci.yml unusedresult funcs lacks %s.%s", pkgPath, name)
		}
		delete(listed, name)
	}
	for name := range listed {
		t.Errorf(".golangci.yml lists %s.%s, which returns no mergedFacts (or no longer exists)", pkgPath, name)
	}
}

func mentionsIdent(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

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
// code: every function in repair or rawentry that RETURNS the values a merge chose away
// (mergedFacts, RecordingLoss or RecordingMove, in any shape) must be listed there, so a call that discards that
// return fails the linter instead of deleting a fact with nothing in the notes to
// say so. A helper added or renamed without the list edit is caught here, and a
// listed name that no longer exists is caught too (it would guard nothing).
//
// The analyzer reaches package-level functions only, so a METHOD returning losses
// is refused outright: it could never be linted. And it sees only a bare call
// STATEMENT - `_ = f()` and `x, _ := f()` are assignments it never looks at - so
// this test refuses a loss result assigned to the blank identifier itself.
func TestLossHelpersAreLinted(t *testing.T) {
	for _, pkg := range []struct {
		name, dir string
		losses    []string
	}{
		{"repair", ".", []string{"mergedFacts"}},
		{"rawentry", "../rawentry", []string{"RecordingLoss", "RecordingMove"}},
	} {
		t.Run(pkg.name, func(t *testing.T) { checkLossHelpersLinted(t, pkg.name, pkg.dir, pkg.losses) })
	}
}

func checkLossHelpersLinted(t *testing.T, pkg, dir string, lossTypes []string) {
	t.Helper()
	pkgPath := "github.com/kodestar/audiosilo-meta/internal/" + pkg
	carriesLoss := func(n ast.Node) bool {
		for _, name := range lossTypes {
			if mentionsIdent(n, name) {
				return true
			}
		}
		return false
	}
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var helpers []string
	// lossAt maps a helper to the result positions that carry a mergedFacts.
	lossAt := map[string][]int{}
	var parsed []*ast.File
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil || !carriesLoss(fn.Type.Results) {
				continue
			}
			if fn.Recv != nil {
				t.Errorf("%s: method %s returns mergedFacts, which unusedresult cannot lint - make it a function", path, fn.Name.Name)
				continue
			}
			helpers = append(helpers, fn.Name.Name)
			i := 0
			for _, field := range fn.Type.Results.List {
				n := max(len(field.Names), 1)
				if carriesLoss(field.Type) {
					for j := range n {
						lossAt[fn.Name.Name] = append(lossAt[fn.Name.Name], i+j)
					}
				}
				i += n
			}
		}
	}
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Rhs) != 1 {
				return true
			}
			call, ok := as.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			for _, i := range lossAt[id.Name] {
				if i < len(as.Lhs) {
					if lhs, ok := as.Lhs[i].(*ast.Ident); ok && lhs.Name == "_" {
						t.Errorf("%s: the losses %s returns are assigned to _ - name them in the notes (txn.noteLost)",
							fset.Position(as.Pos()), id.Name)
					}
				}
			}
			return true
		})
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

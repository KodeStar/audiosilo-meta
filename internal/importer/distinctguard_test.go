package importer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// distinctExemption is the comment that lets an Options literal go without
// Distinct, followed by the reason: a run that plans no credits has no fold to
// guard.
const distinctExemption = "no reviewed decisions:"

// TestEveryOptionsLiteralCarriesDistinct is the guard on Options.Distinct: the
// reviewed merge rejections reach the honorific fold only when the caller puts
// them there, so a new entry point that forgets them re-merges a pair a review
// separated with every test still green. Every Options or SelectOptions literal
// in the module's non-test code must set Distinct, or carry the exemption comment
// on its lines or the line before it.
func TestEveryOptionsLiteralCarriesDistinct(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var literals int
	for _, dir := range []string{"cmd", "internal", "pkg", "scripts"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			inImporter := f.Name.Name == "importer"
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isOptionsType(lit.Type, inImporter) {
					return true
				}
				literals++
				if setsField(lit, "Distinct") || exempted(fset, f, lit) {
					return true
				}
				t.Errorf("%s: an Options literal without Distinct: pass audit.ReviewedDistinct() (or the caller's "+
					"decisions), or say why not with a %q comment", fset.Position(lit.Pos()), "// "+distinctExemption+" <why>")
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if literals == 0 {
		t.Fatal("found no Options literal at all: the scan is looking in the wrong place")
	}
}

// isOptionsType reports whether a literal's type is the importer's Options or
// SelectOptions: the bare name inside this package, importer.<name> elsewhere.
func isOptionsType(typ ast.Expr, inImporter bool) bool {
	named := func(name string) bool { return name == "Options" || name == "SelectOptions" }
	switch e := typ.(type) {
	case *ast.Ident:
		return inImporter && named(e.Name)
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		return ok && x.Name == "importer" && named(e.Sel.Name)
	}
	return false
}

// setsField reports whether a keyed literal sets field.
func setsField(lit *ast.CompositeLit, field string) bool {
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == field {
				return true
			}
		}
	}
	return false
}

// exempted reports whether the exemption comment sits on the literal's lines or
// the line before it.
func exempted(fset *token.FileSet, f *ast.File, lit *ast.CompositeLit) bool {
	from, to := fset.Position(lit.Pos()).Line-1, fset.Position(lit.End()).Line
	for _, g := range f.Comments {
		if line := fset.Position(g.End()).Line; line >= from && line <= to && strings.Contains(g.Text(), distinctExemption) {
			return true
		}
	}
	return false
}

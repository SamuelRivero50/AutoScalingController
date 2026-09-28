package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// allowedImports is the full set of packages the pure core may import.
var allowedImports = map[string]bool{
	"errors": true,
	"fmt":    true,
	"math":   true,
	"slices": true,
	"time":   true,
}

// forbiddenCalls are functions that read the clock or randomness.
var forbiddenCalls = map[string]bool{
	"time.Now":   true,
	"time.Since": true,
	"time.Until": true,
	"time.Sleep": true,
	"time.After": true,
	"time.Tick":  true,
}

// TestCoreIsPure enforces the functional-core rule: stdlib-only allow-listed
// imports, no clock reads, no package-level variables.
func TestCoreIsPure(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowedImports[path] {
				t.Errorf("%s imports %q, not allowed in the pure core", name, path)
			}
		}
		for _, decl := range f.Decls {
			if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.VAR {
				t.Errorf("%s: package-level var at %s", name, fset.Position(g.Pos()))
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && forbiddenCalls[pkg.Name+"."+sel.Sel.Name] {
				t.Errorf("%s: %s.%s at %s", name, pkg.Name, sel.Sel.Name, fset.Position(sel.Pos()))
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source files checked")
	}
}

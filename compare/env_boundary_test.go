package compare

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Only the Rust child's PATH may be read from the process environment.
func TestNoProcessEnvReadsOutsideRustPath(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]bool{"Getenv": true, "LookupEnv": true, "Environ": true, "ExpandEnv": true, "Expand": true}
	allowed := 0
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		osName := ""
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == "os" {
				osName = "os"
				if imp.Name != nil {
					osName = imp.Name.Name
				}
			}
		}
		if osName == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !reads[sel.Sel.Name] {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != osName {
				return true
			}
			if name == "rust.go" && sel.Sel.Name == "Getenv" && onlyPathCall(f, sel) {
				allowed++
				return true
			}
			t.Errorf("%s: os.%s reads the process environment", fset.Position(sel.Pos()), sel.Sel.Name)
			return true
		})
	}
	if allowed != 1 {
		t.Errorf("want exactly one os.Getenv(\"PATH\") in rust.go, found %d", allowed)
	}
}

func onlyPathCall(f *ast.File, sel *ast.SelectorExpr) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Fun != sel || len(call.Args) != 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		found = ok && lit.Kind == token.STRING && lit.Value == `"PATH"`
		return true
	})
	return found
}

// Package archtest holds the rules of how the panel is put together, checked
// against its source: each one a decision the structural review of 3.0 made,
// kept from being undone quietly. docs/development/architecture.md says why.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// root is the repository, two directories up from here.
func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// file is one parsed source file of the panel, tests left out.
type file struct {
	rel string // slash-separated, from the repository
	ast *ast.File
	fs  *token.FileSet
}

func sources(t *testing.T, dirs ...string) []file {
	t.Helper()
	base := root(t)
	var out []file
	for _, d := range dirs {
		err := filepath.WalkDir(filepath.Join(base, d), func(path string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				if n := e.Name(); n == "testdata" || n == "node_modules" || n == "dist" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fs := token.NewFileSet()
			f, err := parser.ParseFile(fs, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(base, path)
			out = append(out, file{rel: filepath.ToSlash(rel), ast: f, fs: fs})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// No package lets another set a function on it.
//
// An exported package-level variable holding a function is a hook: whoever
// sets it last decides what the package does, a path that runs before it is
// set calls nil or the wrong default, and tests that set it run into each
// other. A dependency is given to the instance that needs it instead.
func TestNoPackageHasAFunctionAnotherSets(t *testing.T) {
	for _, f := range sources(t, "internal", "cmd") {
		for _, d := range f.ast.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, spec := range g.Specs {
				v := spec.(*ast.ValueSpec)
				isFunc := false
				if _, ok := v.Type.(*ast.FuncType); ok {
					isFunc = true
				}
				for _, val := range v.Values {
					if _, ok := val.(*ast.FuncLit); ok {
						isFunc = true
					}
				}
				for _, n := range v.Names {
					if isFunc && n.IsExported() {
						t.Errorf("%s:%d: %s is a function another package can set; give it to the instance that needs it",
							f.rel, f.fs.Position(n.Pos()).Line, n.Name)
					}
				}
			}
		}
	}
}

// Only the panel's start writes another package's variable.
//
// A plain setting kept by a package -- where its files go, how often
// something runs -- is set once, by cmd/wui at start, before anything reads
// it. Written from anywhere else it is one package changing how another
// behaves behind its back, which is what the hooks did.
func TestOnlyTheStartWritesAnotherPackagesVariable(t *testing.T) {
	const module = "github.com/abolfazl/w-ui/"
	for _, f := range sources(t, "internal", "cmd", "tools") {
		if strings.HasPrefix(f.rel, "cmd/wui/") || strings.HasPrefix(f.rel, "tools/") {
			continue
		}
		pkgs := map[string]bool{} // the names this file's own packages are imported by
		for _, imp := range f.ast.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, module) {
				continue
			}
			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			pkgs[name] = true
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range as.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if id, ok := sel.X.(*ast.Ident); ok && pkgs[id.Name] && id.Obj == nil {
					t.Errorf("%s:%d: writes %s.%s, another package's variable; only cmd/wui sets one, at start",
						f.rel, f.fs.Position(sel.Pos()).Line, id.Name, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

// No tunnel driver depends on another.
//
// Each protocol's driver is its own: what two of them share is in
// internal/backend/agentkit, so changing one protocol cannot break another,
// and a driver can be taken out without the others noticing.
func TestNoDriverImportsAnother(t *testing.T) {
	const prefix = "github.com/abolfazl/w-ui/internal/backend/"
	for _, f := range sources(t, "internal/backend") {
		parts := strings.Split(f.rel, "/")
		if len(parts) < 4 || !strings.HasSuffix(parts[2], "driver") {
			continue
		}
		own := parts[2]
		for _, imp := range f.ast.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			other, ok := strings.CutPrefix(path, prefix)
			if ok && strings.HasSuffix(other, "driver") && other != own {
				t.Errorf("%s imports %s; what drivers share goes in agentkit", f.rel, path)
			}
		}
	}
}

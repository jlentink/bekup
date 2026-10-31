package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/logging"
)

// TestDetailNamesNotRedacted: Redact blanks every detail whose name
// logging.IsSecretKey takes for a secret (object_key, manifest_key,
// temporary_password, ...), so such a name silently loses its value in the
// audit log. The test walks the non-test Go sources of internal/ and cmd/
// and checks every detail name written as a string literal:
//
//   - keys of audit.Details composite literals, and
//   - index assignments name["key"] = ... to identifiers the same file
//     declares as audit.Details (a literal, make(audit.Details), a var or
//     a parameter of that type).
//
// Rename a flagged detail (object, manifest, temporary_login_issued)
// instead of adding an exception; never record a secret in the first place.
func TestDetailNamesNotRedacted(t *testing.T) {
	seen := 0
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			for _, k := range detailNames(t, p) {
				seen++
				if logging.IsSecretKey(k.name) {
					t.Errorf("%s: audit detail %q is redacted by Redact; rename it", k.pos, k.name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen < 100 {
		t.Fatalf("only %d detail names found: the walk is broken", seen)
	}
}

type detailName struct {
	name, pos string
}

// detailNames returns the literal detail names written in one file.
func detailNames(t *testing.T, path string) []detailName {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	inAudit := f.Name.Name == "audit"
	isDetails := func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			return ok && id.Name == "audit" && x.Sel.Name == "Details"
		case *ast.Ident:
			return inAudit && x.Name == "Details"
		}
		return false
	}
	// Identifiers declared as audit.Details anywhere in the file.
	vars := map[string]bool{}
	declares := func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.CompositeLit:
			return isDetails(x.Type)
		case *ast.CallExpr:
			fn, ok := x.Fun.(*ast.Ident)
			return ok && fn.Name == "make" && len(x.Args) > 0 && isDetails(x.Args[0])
		}
		return false
	}
	var out []detailName
	add := func(lit ast.Expr) {
		bl, ok := lit.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return
		}
		k, err := strconv.Unquote(bl.Value)
		if err == nil {
			out = append(out, detailName{name: k, pos: fset.Position(bl.Pos()).String()})
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range x.Rhs {
				if i < len(x.Lhs) && declares(rhs) {
					if id, ok := x.Lhs[i].(*ast.Ident); ok {
						vars[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			if x.Type != nil && isDetails(x.Type) {
				for _, id := range x.Names {
					vars[id.Name] = true
				}
			}
			for i, v := range x.Values {
				if i < len(x.Names) && declares(v) {
					vars[x.Names[i].Name] = true
				}
			}
		case *ast.Field:
			if isDetails(x.Type) {
				for _, id := range x.Names {
					vars[id.Name] = true
				}
			}
		case *ast.CompositeLit:
			if isDetails(x.Type) {
				for _, el := range x.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						add(kv.Key)
					}
				}
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			ix, ok := lhs.(*ast.IndexExpr)
			if !ok {
				continue
			}
			if id, ok := ix.X.(*ast.Ident); ok && vars[id.Name] {
				add(ix.Index)
			}
		}
		return true
	})
	return out
}

// The walker finds both forms (guards the test itself).
func TestDetailNamesWalker(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import "github.com/jlentink/bekup/internal/audit"

func f(extra audit.Details) {
	details := audit.Details{"name": 1, "object_key": 2}
	details["manifest_key"] = 3
	extra["temporary_password"] = true
	other := map[string]any{}
	other["password"] = 1
}
`
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	found := detailNames(t, p)
	names := make([]string, 0, len(found))
	for _, k := range found {
		names = append(names, k.name)
	}
	slices.Sort(names)
	if got := strings.Join(names, ","); got != "manifest_key,name,object_key,temporary_password" {
		t.Fatalf("found %s", got)
	}
}

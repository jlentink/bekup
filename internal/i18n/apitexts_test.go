package i18n

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// msgKind tells how a constructor treats its text argument.
type msgKind int

const (
	kindFormat msgKind = iota // an fmt format followed by its arguments
	kindPlain                 // a constant text shown as is: no fmt verbs
)

// ctor is a message constructor: a function whose argument at index arg is
// the English text of a translatable message.
type ctor struct {
	arg  int
	kind msgKind
}

// ctorKey names a function: its package name and its name.
type ctorKey struct{ pkg, name string }

// seedCtors are the message constructors of D-238. Functions that pass one
// of their own parameters on as such a text are found by the walk and
// become constructors too.
var seedCtors = map[ctorKey]ctor{
	{"i18n", "M"}:               {0, kindFormat},
	{"i18n", "Errorf"}:          {0, kindFormat},
	{"i18n", "Text"}:            {0, kindPlain},
	{"apierror", "New"}:         {1, kindPlain},
	{"apierror", "Invalid"}:     {1, kindPlain},
	{"apierror", "Newf"}:        {1, kindFormat},
	{"apierror", "Invalidf"}:    {1, kindFormat},
	{"serviceutil", "FieldErr"}: {1, kindFormat},
}

type srcFile struct {
	fset *token.FileSet
	path string
	dir  string
	pkg  string
	file *ast.File
}

// parseSources parses every non-test Go file below internal/ and cmd/.
func parseSources(t *testing.T) []srcFile {
	t.Helper()
	fset := token.NewFileSet()
	var out []srcFile
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			out = append(out, srcFile{fset: fset, path: path, dir: filepath.Dir(path), pkg: f.Name.Name, file: f})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(out) < 300 {
		t.Fatalf("parsed only %d files; the walk is broken", len(out))
	}
	return out
}

// stringConsts collects the string constants of each directory (package
// level and inside functions), by name.
func stringConsts(files []srcFile) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, f := range files {
		ast.Inspect(f.file, func(n ast.Node) bool {
			gd, ok := n.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				return true
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if s, ok := literal(vs.Values[i], nil); ok {
						if out[f.dir] == nil {
							out[f.dir] = map[string]string{}
						}
						out[f.dir][name.Name] = s
					}
				}
			}
			return true
		})
	}
	return out
}

// literal evaluates a constant string expression: a literal, a sum of
// constant strings, or a string constant of the same directory.
func literal(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return literal(v.X, consts)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		a, ok1 := literal(v.X, consts)
		b, ok2 := literal(v.Y, consts)
		return a + b, ok1 && ok2
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	}
	return "", false
}

// calleeKey returns the constructor key a call refers to: pkg.Name for a
// selector on a package name, or the file's own package for a plain name.
func calleeKey(call *ast.CallExpr, pkg string) (ctorKey, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return ctorKey{pkg, fn.Name}, true
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return ctorKey{x.Name, fn.Sel.Name}, true
		}
	}
	return ctorKey{}, false
}

// paramIndex returns the index of the parameter called name, or -1.
func paramIndex(fd *ast.FuncDecl, name string) int {
	i := 0
	for _, field := range fd.Type.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, n := range field.Names {
			if n.Name == name {
				return i
			}
			i++
		}
	}
	return -1
}

// findCtors extends the seed constructors with every function that passes
// one of its parameters on as a message text, until nothing changes.
func findCtors(files []srcFile) map[ctorKey]ctor {
	ctors := map[ctorKey]ctor{}
	for k, v := range seedCtors {
		ctors[k] = v
	}
	for changed := true; changed; {
		changed = false
		for _, f := range files {
			for _, decl := range f.file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil || fd.Recv != nil {
					continue
				}
				self := ctorKey{f.pkg, fd.Name.Name}
				if _, seeded := seedCtors[self]; seeded {
					continue
				}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					key, ok := calleeKey(call, f.pkg)
					if !ok {
						return true
					}
					c, ok := ctors[key]
					if !ok || c.arg >= len(call.Args) {
						return true
					}
					id, ok := call.Args[c.arg].(*ast.Ident)
					if !ok {
						return true
					}
					if idx := paramIndex(fd, id.Name); idx >= 0 {
						if _, known := ctors[self]; !known {
							ctors[self] = ctor{arg: idx, kind: c.kind}
							changed = true
						}
					}
					return true
				})
			}
		}
	}
	return ctors
}

// apiFormats collects every message text passed to a constructor, and the
// problems: non-constant texts and '%' in plain texts.
func apiFormats(t *testing.T) (map[string][]string, []string) {
	t.Helper()
	files := parseSources(t)
	consts := stringConsts(files)
	ctors := findCtors(files)
	formats := map[string][]string{}
	var problems []string
	for _, f := range files {
		for _, decl := range f.file.Decls {
			fd, isFunc := decl.(*ast.FuncDecl)
			var wrapper bool
			if isFunc && fd.Recv == nil {
				_, wrapper = ctors[ctorKey{f.pkg, fd.Name.Name}]
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				if lit, ok := n.(*ast.CompositeLit); ok && f.pkg != "i18n" && len(lit.Elts) > 0 {
					if sel, ok := lit.Type.(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok && x.Name == "i18n" && sel.Sel.Name == "Msg" {
							problems = append(problems, f.path+": i18n.Msg literal; use i18n.M")
						}
					}
				}
				if lit, ok := n.(*ast.CompositeLit); ok && messageWithoutMsg(lit) {
					problems = append(problems, fmt.Sprintf("%s: an error literal sets Message without Msg; use the package's constructor",
						f.fset.Position(lit.Pos())))
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				key, ok := calleeKey(call, f.pkg)
				if !ok {
					return true
				}
				c, ok := ctors[key]
				if !ok || c.arg >= len(call.Args) {
					return true
				}
				arg := call.Args[c.arg]
				at := f.fset.Position(call.Pos()).String()
				if id, ok := arg.(*ast.Ident); ok && wrapper && paramIndex(fd, id.Name) >= 0 {
					return true // a forwarding wrapper; its callers are checked
				}
				s, ok := literal(arg, consts[f.dir])
				if !ok {
					problems = append(problems, fmt.Sprintf("%s: %s.%s: the text is not a constant", at, key.pkg, key.name))
					return true
				}
				for _, a := range call.Args[c.arg+1:] {
					if passesText(a) {
						problems = append(problems, fmt.Sprintf("%s: %s.%s(%q): an argument passes another text on (.Error(), .Message or .Reason); pass its Msg", at, key.pkg, key.name, s))
					}
				}
				if !hasWords(s) {
					problems = append(problems, fmt.Sprintf("%s: %s.%s(%q): a text without words passes another text on; give the source a Msg (i18n.Errorf)", at, key.pkg, key.name, s))
				}
				if c.kind == kindPlain && len(verbs(s)) > 0 {
					problems = append(problems, fmt.Sprintf("%s: %s.%s(%q): a formatting verb in a plain text; use the f variant", at, key.pkg, key.name, s))
				}
				formats[s] = append(formats[s], at)
				return true
			})
		}
	}
	return formats, problems
}

// messageWithoutMsg reports whether lit is a keyed literal of a type
// named *Error that sets Message but not Msg: such an error reaches users
// in English only. Types named APIError (the external API's JSON body,
// English by D-220) and gqlerror.Error (already rendered) are exempt.
func messageWithoutMsg(lit *ast.CompositeLit) bool {
	var name string
	switch t := lit.Type.(type) {
	case *ast.Ident:
		name = t.Name
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok && x.Name == "gqlerror" {
			return false // the rendered GraphQL error
		}
		name = t.Sel.Name
	}
	if !strings.HasSuffix(name, "Error") || name == "APIError" {
		return false
	}
	var message, msg bool
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok {
				message = message || k.Name == "Message"
				msg = msg || k.Name == "Msg"
			}
		}
	}
	return message && !msg
}

// passesText reports whether a message argument is the English text of
// something else: err.Error(), x.Message or x.Reason.
func passesText(e ast.Expr) bool {
	if call, ok := e.(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(call.Args) == 0 {
			return true
		}
	}
	if sel, ok := e.(*ast.SelectorExpr); ok {
		return sel.Sel.Name == "Message" || sel.Sel.Name == "Reason"
	}
	return false
}

// hasWords reports whether a format has text of its own besides its
// formatting verbs ("%s" or "%s: %s" only pass other texts on).
func hasWords(format string) bool {
	rest := verbRE.ReplaceAllString(format, "")
	return strings.IndexFunc(rest, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }) >= 0
}

// TestAPITextsHaveDutch is the D-238 source walk: every message text
// passed to a constructor is a constant with a Dutch entry, and every
// Dutch entry is used.
func TestAPITextsHaveDutch(t *testing.T) {
	t.Parallel()
	formats, problems := apiFormats(t)
	if len(formats) < 50 {
		t.Fatalf("found only %d message texts; the walk is broken", len(formats))
	}
	for _, p := range problems {
		t.Error(p)
	}
	var missing []string
	for s, where := range formats {
		if _, ok := apiNL[s]; !ok {
			missing = append(missing, fmt.Sprintf("%q (%s)", s, where[0]))
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("no Dutch text for %s", m)
	}
	var unused []string
	for k := range apiNL {
		if _, ok := formats[k]; !ok {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	for _, k := range unused {
		t.Errorf("Dutch text %q is not used", k)
	}
	if path := os.Getenv("BEKUP_DUMP_API_TEXTS"); path != "" {
		dumpMissing(t, formats, path)
	}
}

// dumpMissing writes the texts without a Dutch entry as Go map lines to
// path, to help a translator (BEKUP_DUMP_API_TEXTS=/tmp/x go test -run
// TestAPITextsHaveDutch ./internal/i18n).
func dumpMissing(t *testing.T, formats map[string][]string, path string) {
	var keys []string
	for s := range formats {
		if _, ok := apiNL[s]; !ok {
			keys = append(keys, s)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "\t%s: %s, // %s\n", strconv.Quote(k), strconv.Quote(k), formats[k][0])
	}
	//nolint:gosec // G703: the path is the developer's own BEKUP_DUMP_API_TEXTS
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Errorf("dump: %v", err)
	}
}

func TestAPICatalogParity(t *testing.T) {
	t.Parallel()
	for _, p := range APICatalog().Problems() {
		t.Error(p)
	}
}

func TestMsgIn(t *testing.T) {
	t.Parallel()
	inner := M("the connection is not ready")
	if got := M("%s: %d jobs", inner, 3).In(EN); got != "the connection is not ready: 3 jobs" {
		t.Errorf("EN = %q", got)
	}
	if got := M("unknown format %d", 1).In(NL); got != "unknown format 1" {
		t.Errorf("fallback = %q", got)
	}
	if (Msg{}).In(NL) != "" || !(Msg{}).IsZero() {
		t.Error("zero Msg")
	}
	err := fmt.Errorf("wrapped: %w", Errorf("at most %d entries", 5))
	m, ok := MsgOf(err)
	if !ok || m.Format != "at most %d entries" || m.String() != "at most 5 entries" {
		t.Errorf("MsgOf = %+v, %v", m, ok)
	}
	if _, ok := MsgOf(errors.New("plain")); ok {
		t.Error("MsgOf(plain error)")
	}
}

func TestMsgNestedJoinText(t *testing.T) {
	t.Parallel()
	nested := M("the connection is not ready: %s", M("set the password"))
	if got, want := nested.In(NL), apiNL["the connection is not ready: %s"]; got == nested.In(EN) || !strings.HasPrefix(got, strings.TrimSuffix(want, "%s")) {
		t.Errorf("nested NL = %q", got)
	}
	if !strings.HasSuffix(nested.In(NL), apiNL["set the password"]) {
		t.Errorf("inner message not translated: %q", nested.In(NL))
	}
	j := Join("; ", M("set the password"), Text("100% sure"))
	if got := j.In(EN); got != "set the password; 100% sure" {
		t.Errorf("Join EN = %q", got)
	}
	if got := Join("; ", M("a %d", 1)); got.String() != "a 1" {
		t.Errorf("Join of one = %q", got.String())
	}
	if !Join("; ").IsZero() {
		t.Error("Join of none is not zero")
	}
	if got := Join("50%", Text("a"), Text("b")).String(); got != "a50%b" {
		t.Errorf("Join escapes the separator: %q", got)
	}
	if got := Text("encode spaces as %20").In(NL); got != "encode spaces as %20" {
		t.Errorf("Text is not formatted: %q", got)
	}
}

func TestMsgsOf(t *testing.T) {
	t.Parallel()
	a, b := Errorf("problem %d", 1), Errorf("problem %d", 2)
	outer := fmt.Errorf("%w: %w", errors.New("sentinel"), errors.Join(a, fmt.Errorf("ctx: %w", b)))
	got := MsgsOf(outer)
	if len(got) != 2 || got[0].String() != "problem 1" || got[1].String() != "problem 2" {
		t.Errorf("MsgsOf = %v", got)
	}
	if len(MsgsOf(nil)) != 0 || len(MsgsOf(errors.New("plain"))) != 0 {
		t.Error("MsgsOf of nil or a plain error")
	}
	if got := MsgsOf(ErrorOf(M("x %d", 3))); len(got) != 1 || got[0].String() != "x 3" {
		t.Errorf("ErrorOf = %v", got)
	}
}

func TestWalkHelpers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		format string
		words  bool
	}{{"%s", false}, {"%s: %s", false}, {"%d of %d", true}, {"at most %d", true}, {"", false}} {
		if got := hasWords(tc.format); got != tc.words {
			t.Errorf("hasWords(%q) = %v", tc.format, got)
		}
	}
	for src, want := range map[string]bool{"err.Error()": true, "e.Message": true, "r.Reason": true, "r.Reason.Msg()": false, "name": false, `"x"`: false} {
		e, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		if got := passesText(e); got != want {
			t.Errorf("passesText(%s) = %v", src, got)
		}
	}
}

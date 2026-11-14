package laws_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// skipMethods are the methods of testing.T, testing.B and testing.TB that end
// a test early without failing it.
var skipMethods = []string{"Skip", "Skipf", "SkipNow"}

// omissionFindings parses one Go source and describes every way it has to
// leave a test out of a run: a skip method or testing.Short named at all, even
// as a value (f := t.Skip), a dot import of testing, the short flag looked up
// by any constant or spelled out, a build constraint that excludes the file,
// and a TestMain that exits without calling Run.
func omissionFindings(name, source string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var findings []string
	report := func(pos token.Pos, what string) {
		findings = append(findings, name+":"+strconv.Itoa(fset.Position(pos).Line)+": "+what)
	}

	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:build") || strings.HasPrefix(comment.Text, "// +build") {
				report(comment.Pos(), "build constraint that can exclude the tests")
			}
		}
	}
	for _, spec := range file.Imports {
		if spec.Name != nil && spec.Name.Name == "." && strings.Trim(spec.Path.Value, "\"`") == "testing" {
			report(spec.Pos(), "dot import of testing hides calls to Skip and Short")
		}
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "TestMain" && fn.Body != nil {
			runs := false
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "Run" {
					runs = true
				}
				return true
			})
			if !runs {
				report(fn.Pos(), "TestMain never calls Run")
			}
		}
	}
	selected := map[*ast.Ident]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		if n, ok := node.(*ast.SelectorExpr); ok {
			selected[n.Sel] = true
		}
		return true
	})
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			// A lookup of any flag whose name is not spelled out can be the short one.
			if selector, ok := n.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Lookup" && len(n.Args) == 1 {
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "flag" {
					if _, literal := n.Args[0].(*ast.BasicLit); !literal {
						report(n.Pos(), "flag.Lookup with a name that is not a literal")
					}
				}
			}
		case *ast.SelectorExpr:
			switch {
			case slices.Contains(skipMethods, n.Sel.Name):
				report(n.Pos(), "use of "+n.Sel.Name)
			case n.Sel.Name == "Short":
				report(n.Pos(), "use of Short")
			}
		case *ast.Ident:
			// With a dot import the names stand alone.
			if !selected[n] && (n.Name == "Short" || slices.Contains(skipMethods, n.Name)) {
				report(n.Pos(), "use of "+n.Name)
			}
		case *ast.BasicLit:
			if value, err := strconv.Unquote(n.Value); err == nil && n.Kind == token.STRING && value == "test."+"short" {
				report(n.Pos(), "mention of the test.short flag")
			}
		}
		return true
	})
	return findings, nil
}

// lawPrefixes are the test name prefixes that stand for each of the five laws.
// The guard needs at least one test function per prefix.
var lawPrefixes = map[string]string{
	"conservation": "TestLawConservation",
	"memory":       "TestLawMemory",
	"commitment":   "TestLawCommitment",
	"permission":   "TestLawPermission",
	"motion":       "TestLawMotion",
}

// TestLawsPackageHasNoOmissions guards criterion 1: no test of this package
// can be skipped, shortened or compiled out, and every law has a test. It
// reads the sources of the package from disk, this file included.
func TestLawsPackageHasNoOmissions(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if len(paths) < 5 {
		t.Fatalf("found %d sources, the guard is not looking at the package: %v", len(paths), paths)
	}
	tests := map[string]bool{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		findings, err := omissionFindings(path, string(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, finding := range findings {
			t.Errorf("%s", finding)
		}
		for name := range testFunctions(t, path, string(raw)) {
			tests[name] = true
		}
	}
	for law, prefix := range lawPrefixes {
		found := false
		for name := range tests {
			if strings.HasPrefix(name, prefix) {
				found = true
			}
		}
		if !found {
			t.Errorf("no test function named %s* for the law of %s", prefix, law)
		}
	}
}

// testFunctions returns the names of the Test functions of a source that Go
// would run: top-level functions of a _test.go file with the single parameter
// *testing.T.
func testFunctions(t *testing.T, name, source string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	if !strings.HasSuffix(name, "_test.go") {
		return names
	}
	file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Type.Params.NumFields() != 1 {
			continue
		}
		star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if selector, ok := star.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "T" {
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "testing" {
				names[fn.Name.Name] = true
			}
		}
	}
	return names
}

// TestOmissionGuardDetectsOmissions proves the guard is not vacuous: every
// way of leaving a test out is reported on a synthetic source, and a clean
// source is not.
func TestOmissionGuardDetectsOmissions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   int
	}{
		{"skip", "package p\nfunc f(t T) { t.Skip(\"x\") }", 1},
		{"skipf", "package p\nfunc f(t T) { t.Skipf(\"x\") }", 1},
		{"skip now", "package p\nfunc f(t T) { t.SkipNow() }", 1},
		{"skip as a value", "package p\nfunc f(t T) { g := t.Skip; g() }", 1},
		{"short", "package p\nfunc f() { if testing.Short() { return } }", 1},
		{"short as a value", "package p\nfunc f() { s := testing.Short; _ = s }", 1},
		{"short flag literal", "package p\nfunc f() { flag.Lookup(\"test.short\") }", 1},
		{"short flag in a variable", "package p\nconst name = \"test.short\"\nfunc f() { flag.Lookup(name) }", 2},
		{"dot import", "package p\nimport . \"testing\"\nfunc f() { if Short() { return } }", 2},
		{"main that never runs", "package p\nfunc TestMain(m *testing.M) { os.Exit(0) }", 1},
		{"main that runs", "package p\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }", 0},
		{"build constraint", "//go:build integration\n\npackage p\n", 1},
		{"old build constraint", "// +build integration\n\npackage p\n", 1},
		{"two at once", "package p\nfunc f(t T) { t.Skip(); testing.Short() }", 2},
		{"clean", "package p\nfunc f(t T) { t.Fatal(\"x\"); t.Run(\"a\", nil) }", 0},
	}
	if got := testFunctions(t, "a_test.go", "package p\nfunc TestLawX(t *testing.T) {}\nfunc TestLawY(t *testing.B) {}\nfunc TestLawZ() {}\nfunc (r R) TestLawW(t *testing.T) {}"); len(got) != 1 || !got["TestLawX"] {
		t.Errorf("test functions = %v, want only TestLawX", got)
	}
	if got := testFunctions(t, "a.go", "package p\nfunc TestLawX(t *testing.T) {}"); len(got) != 0 {
		t.Errorf("a file that is not a test file counted as tests: %v", got)
	}
	for _, c := range cases {
		findings, err := omissionFindings("sample.go", c.source)
		if err != nil {
			t.Fatalf("%s: parse: %v", c.name, err)
		}
		if len(findings) != c.want {
			t.Errorf("%s: %d findings %v, want %d", c.name, len(findings), findings, c.want)
		}
	}
}

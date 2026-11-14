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
// leave a test out of a run: a skip call, a use of testing.Short, a lookup of
// the short flag and a build constraint that excludes the file.
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
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch {
		case slices.Contains(skipMethods, selector.Sel.Name):
			report(call.Pos(), "call to "+selector.Sel.Name)
		case selector.Sel.Name == "Short":
			report(call.Pos(), "call to Short")
		case selector.Sel.Name == "Lookup" && len(call.Args) == 1 && isStringLiteral(call.Args[0], "test.short"):
			report(call.Pos(), "lookup of the test.short flag")
		}
		return true
	})
	return findings, nil
}

// isStringLiteral reports whether expr is the string literal want.
func isStringLiteral(expr ast.Expr, want string) bool {
	literal, ok := expr.(*ast.BasicLit)
	return ok && literal.Kind == token.STRING && strings.Trim(literal.Value, "\"`") == want
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

// testFunctions returns the names of the Test functions of a source.
func testFunctions(t *testing.T, name, source string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	names := map[string]bool{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
			names[fn.Name.Name] = true
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
		{"short", "package p\nfunc f() { if testing.Short() { return } }", 1},
		{"short flag", "package p\nfunc f() { flag.Lookup(\"test.short\") }", 1},
		{"build constraint", "//go:build integration\n\npackage p\n", 1},
		{"old build constraint", "// +build integration\n\npackage p\n", 1},
		{"two at once", "package p\nfunc f(t T) { t.Skip(); testing.Short() }", 2},
		{"clean", "package p\nfunc f(t T) { t.Fatal(\"x\"); t.Run(\"a\", nil) }", 0},
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

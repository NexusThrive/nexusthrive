package kernel_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// forbiddenImportPrefixes are the layers the kernel must never depend on.
// Extensions register themselves through kernel/extension instead (ADR-0001).
var forbiddenImportPrefixes = []string{
	"github.com/nexusthrive/nexusthrive/platform",
	"github.com/nexusthrive/nexusthrive/distributions",
	"github.com/nexusthrive/nexusthrive/services",
}

// forbiddenWords is the business vocabulary the kernel must not learn. Each
// entry is matched as a whole word inside an identifier, in singular or plural.
var forbiddenWords = []string{
	"customer",
	"invoice",
	"sale",
	"membership",
	"gym",
	"restaurant",
}

// stateImportPath is the package that persists kernel state. Only the engine
// may import it (ADR-0035): every other writer would be a road around the
// permission check.
const stateImportPath = "github.com/nexusthrive/nexusthrive/kernel/state"

// stateImporterDirs are the directories, relative to the repository root and
// in slash form, whose non-test files may import kernel/state.
var stateImporterDirs = []string{"kernel/state", "kernel/execution"}

// selfFile is excluded from the scan because it names the forbidden words.
const selfFile = "architecture_test.go"

func TestKernelDoesNotImportOtherLayers(t *testing.T) {
	for _, file := range parseKernelFiles(t) {
		for _, violation := range forbiddenImports(file.ast) {
			t.Errorf("%s: kernel must not import %q", file.path, violation)
		}
	}
}

func TestKernelDoesNotLearnBusinessVocabulary(t *testing.T) {
	for _, file := range parseKernelFiles(t) {
		for _, violation := range forbiddenIdentifiers(file.ast) {
			t.Errorf("%s: identifier %q uses business word %q", file.path, violation.identifier, violation.word)
		}
	}
}

// sqlWordPattern extracts the words of a SQL file: identifiers, strings and
// comments alike, because none of them may carry business vocabulary.
var sqlWordPattern = regexp.MustCompile(`[a-z]+`)

// TestMigrationsDoNotLearnBusinessVocabulary scans the kernel migrations,
// comments and string literals included, for the forbidden words.
func TestMigrationsDoNotLearnBusinessVocabulary(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("db", "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no kernel migrations found to scan")
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, word := range forbiddenSQLWords(string(raw)) {
			t.Errorf("%s: uses business word %q", path, word)
		}
	}
}

// TestSQLScanDetectsViolations proves the SQL scan is not vacuous.
func TestSQLScanDetectsViolations(t *testing.T) {
	const source = "CREATE TABLE kernel.Invoices (id int); -- the customer pays\nSELECT 'gym';"
	got := forbiddenSQLWords(source)
	slices.Sort(got)
	if want := []string{"customer", "gym", "invoice"}; !slices.Equal(got, want) {
		t.Errorf("expected words %v, got %v", want, got)
	}
}

// forbiddenSQLWords returns the forbidden words found in SQL text, whole words
// only, singular or plural, case-insensitive.
func forbiddenSQLWords(sql string) []string {
	var found []string
	for _, word := range sqlWordPattern.FindAllString(strings.ToLower(sql), -1) {
		if match, ok := matchForbiddenWord(word); ok {
			found = append(found, match)
		}
	}
	return found
}

// TestArchitectureRulesDetectViolations proves the checks above are not
// vacuous: a source that breaks both rules must be reported.
func TestArchitectureRulesDetectViolations(t *testing.T) {
	const source = `package sample

import _ "github.com/nexusthrive/nexusthrive/platform/staff"

type CustomerInvoice struct{ membership_id string }

func ApplySales() {}
`
	parsed, err := parser.ParseFile(token.NewFileSet(), "sample.go", source, 0)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}

	if got := forbiddenImports(parsed); len(got) != 1 {
		t.Errorf("expected 1 forbidden import, got %v", got)
	}

	var words []string
	for _, violation := range forbiddenIdentifiers(parsed) {
		words = append(words, violation.word)
	}
	slices.Sort(words)
	if want := []string{"customer", "invoice", "membership", "sale"}; !slices.Equal(words, want) {
		t.Errorf("expected words %v, got %v", want, words)
	}
}

// TestKernelHasGoFilesToScan guards against the scan silently covering nothing.
func TestKernelHasGoFilesToScan(t *testing.T) {
	if len(parseKernelFiles(t)) == 0 {
		t.Fatal("no Go files found in the kernel module")
	}
}

type parsedFile struct {
	path string
	ast  *ast.File
}

// parseKernelFiles parses every Go file of the kernel module, skipping this
// test file. The test runs with the kernel directory as working directory.
func parseKernelFiles(t *testing.T) []parsedFile {
	t.Helper()

	var files []parsedFile
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || filepath.Base(path) == selfFile {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		files = append(files, parsedFile{path: path, ast: parsed})
		return nil
	})
	if err != nil {
		t.Fatalf("scan kernel sources: %v", err)
	}
	return files
}

func forbiddenImports(file *ast.File) []string {
	var violations []string
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		for _, prefix := range forbiddenImportPrefixes {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				violations = append(violations, path)
			}
		}
	}
	return violations
}

type vocabularyViolation struct {
	identifier string
	word       string
}

func forbiddenIdentifiers(file *ast.File) []vocabularyViolation {
	var violations []vocabularyViolation
	ast.Inspect(file, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		for _, word := range splitWords(ident.Name) {
			if match, found := matchForbiddenWord(word); found {
				violations = append(violations, vocabularyViolation{identifier: ident.Name, word: match})
			}
		}
		return true
	})
	return violations
}

// matchForbiddenWord reports the forbidden word a lowercase word stands for,
// accepting a plural form.
func matchForbiddenWord(word string) (string, bool) {
	for _, forbidden := range forbiddenWords {
		if word == forbidden || word == forbidden+"s" {
			return forbidden, true
		}
	}
	return "", false
}

// splitWords breaks an identifier into lowercase words on case changes,
// digits and underscores: "ApplySales" yields "apply" and "sales".
func splitWords(name string) []string {
	var words []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			words = append(words, strings.ToLower(string(current)))
			current = current[:0]
		}
	}

	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '_' || unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r):
			// Start a new word at "aB" and at the last capital of "ABc".
			startsWord := i > 0 && (unicode.IsLower(runes[i-1]) ||
				(unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])))
			if startsWord {
				flush()
			}
			current = append(current, r)
		default:
			current = append(current, r)
		}
	}
	flush()
	return words
}

// stateImportViolations reports the imports of kernel/state, or of a package
// under it, in a non-test file whose path (relative to the repository root, in
// slash form) is outside the directories that may import it. A directory
// counts only as a whole path segment: kernel/executionx is not
// kernel/execution.
func stateImportViolations(path string, file *ast.File) []string {
	if strings.HasSuffix(path, "_test.go") {
		return nil
	}
	for _, dir := range stateImporterDirs {
		if strings.HasPrefix(path, dir+"/") {
			return nil
		}
	}
	var violations []string
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if imported == stateImportPath || strings.HasPrefix(imported, stateImportPath+"/") {
			violations = append(violations, imported)
		}
	}
	return violations
}

// TestOnlyExecutionImportsState guards the road around the permission check:
// outside kernel/state and kernel/execution no non-test Go file of the
// repository, platform, distributions and services included, imports
// kernel/state.
func TestOnlyExecutionImportsState(t *testing.T) {
	root := ".."
	fileSet := token.NewFileSet()
	scanned := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".claude", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		scanned[relative] = true
		for _, violation := range stateImportViolations(relative, parsed) {
			t.Errorf("%s: only kernel/execution may import %q", relative, violation)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan the repository: %v", err)
	}
	// The scan must see the importer that is allowed and the layers that are
	// forbidden, or it proves nothing.
	for _, want := range []string{"kernel/execution/engine.go", "kernel/state/exchange.go", "kernel/capability/authorizer.go", "services/api/cmd/api/main.go"} {
		if !scanned[want] {
			t.Errorf("the scan did not reach %s", want)
		}
	}
}

// TestStateImportRuleDetectsViolations proves the rule above is not vacuous:
// synthetic sources placed at different paths are reported or accepted as the
// rule says.
func TestStateImportRuleDetectsViolations(t *testing.T) {
	const importing = `package sample

import "github.com/nexusthrive/nexusthrive/kernel/state"

var _ = state.InSpace
`
	const aliased = `package sample

import _ "github.com/nexusthrive/nexusthrive/kernel/state"
`
	const subpackage = `package sample

import . "github.com/nexusthrive/nexusthrive/kernel/state/internal"
`
	const harmless = `package sample

import "github.com/nexusthrive/nexusthrive/kernel/model"
`
	cases := []struct {
		name   string
		path   string
		source string
		want   int
	}{
		{"capability imports state", "kernel/capability/authorizer.go", importing, 1},
		{"platform imports state", "platform/staff/staff.go", importing, 1},
		{"a distribution imports state", "distributions/business/base/base.go", importing, 1},
		{"a service imports state", "services/api/cmd/api/main.go", importing, 1},
		{"blank import", "kernel/laws/laws.go", aliased, 1},
		{"dot import of a subpackage", "kernel/laws/laws.go", subpackage, 1},
		{"a directory that only starts like the engine", "kernel/executionx/engine.go", importing, 1},
		{"a directory that only starts like state", "kernel/statex/state.go", importing, 1},
		{"a file at the root of kernel", "kernel/doc.go", importing, 1},
		{"the engine", "kernel/execution/engine.go", importing, 0},
		{"state itself", "kernel/state/exchange.go", importing, 0},
		{"a test file", "kernel/capability/authorizer_test.go", importing, 0},
		{"a harmless import", "kernel/capability/authorizer.go", harmless, 0},
	}
	for _, c := range cases {
		parsed, err := parser.ParseFile(token.NewFileSet(), "sample.go", c.source, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: parse: %v", c.name, err)
		}
		if got := stateImportViolations(c.path, parsed); len(got) != c.want {
			t.Errorf("%s: %d violations %v, want %d", c.name, len(got), got, c.want)
		}
	}
}

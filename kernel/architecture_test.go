package kernel_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
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

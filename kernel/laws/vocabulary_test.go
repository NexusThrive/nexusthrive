package laws_test

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// businessWords is the vocabulary of the worlds built on top of the kernel
// (AGENTS.md, docs/01-modelo). Each is matched as a whole word, singular or
// plural. The architecture test already reads Go identifiers; this list is
// read in comments, strings and SQL as well. "store" is left out on purpose:
// it is an ordinary verb in database code.
var businessWords = []string{
	"customer", "invoice", "sale", "membership", "gym", "restaurant",
	"cliente", "factura", "venta", "membresia", "membresía", "socio",
}

// vocabularyExempt are the files that name the words in order to forbid them.
var vocabularyExempt = []string{"architecture_test.go", "vocabulary_test.go"}

var (
	wordPattern = regexp.MustCompile(`\p{L}+`)
	// camelBoundary splits identifiers such as ApplySales into words.
	camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// businessWordsIn returns the forbidden words found in text, whole words only.
func businessWordsIn(text string) []string {
	var found []string
	for _, word := range wordPattern.FindAllString(strings.ToLower(camelBoundary.ReplaceAllString(text, "$1 $2")), -1) {
		for _, forbidden := range businessWords {
			if word == forbidden || word == forbidden+"s" {
				found = append(found, forbidden)
			}
		}
	}
	return found
}

// goSourceWords scans a Go source for forbidden words in every token that
// carries text: comments, string and character literals, and identifiers.
func goSourceWords(name, source string) []string {
	fset := token.NewFileSet()
	file := fset.AddFile(name, fset.Base(), len(source))
	var s scanner.Scanner
	s.Init(file, []byte(source), nil, scanner.ScanComments)
	var found []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return found
		}
		switch tok {
		case token.COMMENT, token.STRING, token.CHAR, token.IDENT:
			found = append(found, businessWordsIn(lit)...)
		}
	}
}

// TestKernelCommentsStringsAndSQLHaveNoBusinessVocabulary guards criterion 7
// of the closing story: the architecture test reads identifiers, and this one
// reads what it cannot, the comments and strings of every Go file of the
// kernel, tests included, and every SQL file. It must find no word of
// businessWords.
func TestKernelCommentsStringsAndSQLHaveNoBusinessVocabulary(t *testing.T) {
	t.Parallel()
	root := ".."
	var visited int
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || slices.Contains(vocabularyExempt, entry.Name()) {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".sql" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		visited++
		var found []string
		if ext == ".go" {
			found = goSourceWords(path, string(raw))
		} else {
			found = businessWordsIn(string(raw))
		}
		for _, word := range found {
			t.Errorf("%s uses the business word %q", path, word)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the kernel: %v", err)
	}
	if visited < 80 {
		t.Fatalf("the scan visited %d files, it is not looking at the kernel", visited)
	}
}

// TestBusinessVocabularyScanDetectsWords proves the scan is not vacuous: each
// kind of text that can carry a word is reported, and ordinary database words
// are not.
func TestBusinessVocabularyScanDetectsWords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   []string
	}{
		{"comment", "package p\n// the customer pays\n", []string{"customer"}},
		{"block comment", "package p\n/* an Invoice */\n", []string{"invoice"}},
		{"string", "package p\nvar x = \"gym\"\n", []string{"gym"}},
		{"raw string plural", "package p\nvar x = `two sales`\n", []string{"sale"}},
		{"identifier in camel case", "package p\nfunc ApplyMembership() {}\n", []string{"membership"}},
		{"snake case identifier", "package p\nvar restaurant_id int\n", []string{"restaurant"}},
		{"spanish", "package p\nvar x = \"membresía del cliente\"\n", []string{"membresía", "cliente"}},
		{"clean", "package p\n// a participant joins a space\nvar x = \"salesforce salary resale\"\n", nil},
	}
	for _, c := range cases {
		if got := goSourceWords("sample.go", c.source); !slices.Equal(got, c.want) {
			t.Errorf("%s: found %v, want %v", c.name, got, c.want)
		}
	}
	if got := businessWordsIn("CREATE TABLE kernel.invoices (id int); -- a gym"); !slices.Equal(got, []string{"invoice", "gym"}) {
		t.Errorf("SQL: found %v, want invoice and gym", got)
	}
}

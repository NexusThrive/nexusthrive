package db_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestADR0024Exists guards acceptance criterion 1: the migration tool must be
// decided in an accepted ADR that compares the candidate tools.
func TestADR0024Exists(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "05-decisiones", "0024-herramienta-de-migraciones.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ADR-0024: %v", err)
	}
	text := strings.ToLower(string(raw))
	for _, want := range []string{"aceptada", "goose", "atlas", "golang-migrate"} {
		if !strings.Contains(text, want) {
			t.Errorf("ADR-0024 must mention %q", want)
		}
	}
}

package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestVisibilityGate fails if anything reads the assets table without going through
// visible_assets(viewer): only the schema and the maintenance queries listed below may name the
// table directly. A line that only fills in items already picked through the gate may say so with a
// trailing "-- gated: <why>". See docs/architecture.md, "The visibility gate".
func TestVisibilityGate(t *testing.T) {
	direct := regexp.MustCompile(`(?i)\b(from|join|update|into)\s+assets\b`)
	// Maintenance of a person's own library (indexing, pairing, planning the move, importing
	// from another app, reading AI provenance, trashing their own items, uploading, grouping faces,
	// re-filing after a date change, writing XMP sidecars) may name the table;
	// nothing a viewer reads can.
	allowed := map[string]bool{
		filepath.Join("internal", "store", "queries", "ingest.sql"):     true,
		filepath.Join("internal", "store", "queries", "pairing.sql"):    true,
		filepath.Join("internal", "store", "queries", "move.sql"):       true,
		filepath.Join("internal", "store", "queries", "import.sql"):     true,
		filepath.Join("internal", "store", "queries", "provenance.sql"): true,
		filepath.Join("internal", "store", "queries", "characters.sql"): true,
		filepath.Join("internal", "store", "queries", "workers.sql"):    true,
		filepath.Join("internal", "store", "queries", "labels.sql"):     true,
		filepath.Join("internal", "store", "queries", "edit.sql"):       true,
		filepath.Join("internal", "store", "queries", "uploads.sql"):    true,
		filepath.Join("internal", "store", "queries", "faces.sql"):      true,
		filepath.Join("internal", "store", "queries", "refile.sql"):     true,
		filepath.Join("internal", "store", "queries", "stacks.sql"):     true,
		filepath.Join("internal", "store", "queries", "vault.sql"):      true,
		filepath.Join("internal", "store", "queries", "household.sql"):  true,
		filepath.Join("internal", "store", "queries", "sidecars.sql"):   true,
	}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			// Generated code mirrors the queries; tests may inspect tables directly.
			if rel == filepath.Join("internal", "store", "db") || rel == filepath.Join("internal", "store", "migrations") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, "_test.go") || !(strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".sql")) || allowed[rel] {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if direct.MatchString(line) && !strings.Contains(line, "-- gated: ") {
				t.Errorf("%s:%d reads assets directly; use visible_assets(viewer): %s", rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

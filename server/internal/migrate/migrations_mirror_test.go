package migrate

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestDbMigrationsMirrorIsInSync keeps the canonical db/migrations copies
// identical to the go:embed source. Migration 0021 shipped in db/migrations
// alone, so token-dance-migrate applied nothing while reporting success and
// the worker ran against missing columns. The embed set may contain extra
// history (0015 was retired, 0016 never lived in db/migrations); the check
// is therefore one-directional over db/migrations.
func TestDbMigrationsMirrorIsInSync(t *testing.T) {
	canonical, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) == 0 {
		t.Fatal("no db/migrations found")
	}
	for _, canonicalPath := range canonical {
		name := filepath.Base(canonicalPath)
		mirrored, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatalf("migration %s missing from internal/migrate/migrations: %v", name, err)
		}
		original, err := os.ReadFile(canonicalPath)
		if err != nil {
			t.Fatalf("read db/migrations %s: %v", name, err)
		}
		if !bytes.Equal(original, mirrored) {
			t.Errorf("migration %s differs between db/migrations and internal/migrate/migrations", name)
		}
	}
}

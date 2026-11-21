package e2e

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/libraries"
	"photostead.com/server/internal/store/db"
)

// TestDiscoverLibraries: a new install sets up each person's folder in the share, and a library for
// each folder mounted under /libraries, once each, without a command line.
func TestDiscoverLibraries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	share, mounts := t.TempDir(), t.TempDir()
	for _, d := range []string{"holidays", "empty", "oldapp/library", "oldapp/thumbs"} {
		if err := os.MkdirAll(filepath.Join(mounts, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(mounts, "holidays", "a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"oldapp/library/b.jpg", "oldapp/thumbs/b.webp"} {
		if err := os.WriteFile(filepath.Join(mounts, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if f, err := libraries.Discover(ctx, s.Q, share, mounts); err != nil || f.Managed != 0 || len(f.Indexed) != 0 {
		t.Fatalf("before the first account: %+v %v", f, err)
	}
	u, err := auth.Setup(ctx, s, "Rich", "rich@example.test", "a long enough password", "Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	// Only another app's library/ is indexed, not its thumbnails: its parent must not become a library too.
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: u.ID, Name: "old-app", Kind: "indexed",
		RootPath: filepath.Join(mounts, "oldapp", "library"), ReadOnly: true, SidecarMode: "shadow", Watch: true}); err != nil {
		t.Fatal(err)
	}
	f, err := libraries.Discover(ctx, s.Q, share, mounts)
	if err != nil || f.Managed != 1 || len(f.Indexed) != 1 || f.Indexed[0].Name != "holidays" || !f.Indexed[0].ReadOnly {
		t.Fatalf("first run: %+v %v", f, err)
	}
	lib, err := s.Q.ManagedLibrary(ctx, u.ID)
	if err != nil || lib.RootPath != filepath.Join(share, "library", u.Username) {
		t.Errorf("their share folder: %+v %v", lib, err)
	}
	if f, err := libraries.Discover(ctx, s.Q, share, mounts); err != nil || f.Managed != 0 || len(f.Indexed) != 0 {
		t.Errorf("second run added %+v %v", f, err)
	}
}

// TestRemoveLibrary: a folder's library goes only when every item in it has a copy elsewhere; its
// items are then served from those copies, the share's first. A person's share folder never goes.
func TestRemoveLibrary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := auth.Setup(ctx, s, "Rich", "rich@example.test", "a long enough password", "Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	share, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: u.ID, Name: "photostead", Kind: "managed",
		RootPath: t.TempDir(), SidecarMode: "beside"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: u.ID, Name: "old-app", Kind: "indexed",
		RootPath: t.TempDir(), ReadOnly: true, SidecarMode: "shadow"})
	if err != nil {
		t.Fatal(err)
	}
	file := func(lib uuid.UUID, rel string, asset uuid.UUID, state string) uuid.UUID {
		var id uuid.UUID
		if err := s.Pool.QueryRow(ctx, `INSERT INTO files (library_id, rel_path, size, mtime, kind, state, asset_id)
			VALUES ($1, $2, 1, now(), 'image', $3, $4) RETURNING id`, lib, rel, state, asset).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	item := func(n int, primary func(uuid.UUID) uuid.UUID) uuid.UUID {
		var id uuid.UUID
		sum := sha256.Sum256(fmt.Appendf(nil, "%d", n))
		if err := s.Pool.QueryRow(ctx, `INSERT INTO assets (owner_id, type, sha256, taken_at, local_date, date_source, date_basis)
			VALUES ($1, 'image', $2, now(), current_date, 'file', 'instant') RETURNING id`, u.ID, sum[:]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pool.Exec(ctx, `UPDATE assets SET primary_file_id = $2 WHERE id = $1`, id, primary(id)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	both := item(1, func(a uuid.UUID) uuid.UUID {
		file(share.ID, "a.jpg", a, "present")
		return file(old.ID, "a.jpg", a, "present")
	})
	onlyOld := item(2, func(a uuid.UUID) uuid.UUID { return file(old.ID, "b.jpg", a, "present") })
	gone := item(3, func(a uuid.UUID) uuid.UUID {
		file(old.ID, "c.jpg", a, "missing")
		return file(share.ID, "c.jpg", a, "present")
	})

	var only libraries.OnlyCopies
	if _, err := libraries.Remove(ctx, s, old); !errors.As(err, &only) || only.Items != 1 {
		t.Fatalf("with one item only there: %v", err)
	}
	inShare := file(share.ID, "b.jpg", onlyOld, "present")
	moved, err := libraries.Remove(ctx, s, old)
	if err != nil || moved != 2 {
		t.Fatalf("removed: %d moved (want 2), %v", moved, err)
	}
	for _, a := range []uuid.UUID{both, onlyOld, gone} {
		var lib uuid.UUID
		if err := s.Pool.QueryRow(ctx, `SELECT f.library_id FROM assets a JOIN files f ON f.id = a.primary_file_id WHERE a.id = $1`, a).Scan(&lib); err != nil || lib != share.ID {
			t.Errorf("item %s: served from %s (%v), want the share", a, lib, err)
		}
	}
	var primary uuid.UUID
	_ = s.Pool.QueryRow(ctx, `SELECT primary_file_id FROM assets WHERE id = $1`, onlyOld).Scan(&primary)
	if primary != inShare {
		t.Errorf("onlyOld's primary = %s, want its copy in the share", primary)
	}
	var left int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM files WHERE library_id = $1`, old.ID).Scan(&left)
	if _, err := s.Q.GetLibrary(ctx, old.ID); err == nil || left != 0 {
		t.Errorf("the library or %d of its files are still there", left)
	}
	if _, err := libraries.Remove(ctx, s, share); err == nil {
		t.Error("removed a person's share folder")
	}
}

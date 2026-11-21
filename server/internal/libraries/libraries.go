// Package libraries sets up where photos live on a new install, without a command line: each
// person's own folder in the photostead share (where uploads and phone backups are filed), and an
// indexed, read-only library for every folder mounted under /libraries.
package libraries

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Managed makes sure a person has their folder in the managed share (root/library/<username>) as a
// library of their own, if the share is there and writable. It returns false when it isn't.
func Managed(ctx context.Context, q *db.Queries, root string, owner uuid.UUID, username string) (bool, error) {
	if root == "" || username == "" {
		return false, nil
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return false, nil // no share mounted
	}
	dir := filepath.Join(root, "library", username)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil // read-only share
	}
	probe := filepath.Join(dir, ".photostead-write-check")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		return false, nil
	}
	_ = os.Remove(probe)
	_, err := q.EnsureManagedLibrary(ctx, db.EnsureManagedLibraryParams{OwnerID: owner, RootPath: dir})
	return err == nil, err
}

// Found is what Discover set up.
type Found struct {
	Managed int
	Indexed []db.Library
}

// Discover gives every account without one its folder in the managed share, and adds each folder
// under mounts (the container's /libraries) that isn't a library yet as an indexed, read-only,
// watched library of the first admin's. Nothing happens before the first account exists.
func Discover(ctx context.Context, q *db.Queries, managedRoot, mounts string) (Found, error) {
	var f Found
	people, err := q.PeopleWithoutShare(ctx)
	if err != nil {
		return f, err
	}
	for _, p := range people {
		ok, err := Managed(ctx, q, managedRoot, p.ID, p.Username)
		if err != nil {
			return f, err
		}
		if ok {
			f.Managed++
		}
	}
	if mounts == "" {
		return f, nil
	}
	entries, err := os.ReadDir(mounts)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	} else if err != nil {
		return f, err
	}
	admin, err := q.FirstAdmin(ctx)
	if err != nil {
		return f, nil // no account yet
	}
	roots, err := q.LibraryRoots(ctx)
	if err != nil {
		return f, err
	}
	for _, e := range entries {
		dir := filepath.Join(mounts, e.Name())
		if e.Name()[0] == '.' || overlaps(dir, roots) {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() { // a folder, or a link to one
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 { // an empty mount point: nothing chosen for it
			continue
		}
		lib, err := q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: admin, Name: e.Name(), Kind: "indexed", RootPath: dir,
			ReadOnly: true, SidecarMode: "shadow", Watch: true})
		if err != nil {
			return f, err
		}
		f.Indexed = append(f.Indexed, lib)
	}
	return f, nil
}

// overlaps says whether dir is a library already, holds one (another app's folder, of which only
// some subfolders are indexed), or is inside one: indexing it would index those files twice, or the
// folders left out on purpose.
func overlaps(dir string, roots []string) bool {
	sep := string(filepath.Separator)
	for _, r := range roots {
		r = filepath.Clean(r)
		if r == dir || strings.HasPrefix(r, dir+sep) || strings.HasPrefix(dir, r+sep) {
			return true
		}
	}
	return false
}

// OnlyCopies is the error Remove gives when a library holds the only copy of some items.
type OnlyCopies struct{ Items int }

func (e OnlyCopies) Error() string {
	return fmt.Sprintf("%d items have no copy outside this library; file them into the share first (photostead move plan), or they'd be lost", e.Items)
}

// Remove stops indexing a library: its items are served from their other copies (the share's
// first), then its records go. Nothing on disk is touched. It refuses (OnlyCopies) when the library
// holds the only copy of any item, and it never removes a person's folder in the share. A folder
// still mounted under /libraries is found again by Discover, so it's unmounted first.
func Remove(ctx context.Context, s *store.Store, lib db.Library) (moved int64, err error) {
	if lib.Kind == "managed" {
		return 0, errors.New("a person's folder in the share can't be removed")
	}
	err = s.Tx(ctx, func(q *db.Queries) error {
		only, err := q.LibraryOnlyCopies(ctx, lib.ID)
		if err != nil {
			return err
		}
		if only > 0 {
			return OnlyCopies{Items: int(only)}
		}
		if moved, err = q.RepointPrimaryFiles(ctx, lib.ID); err != nil {
			return err
		}
		return q.DeleteLibrary(ctx, lib.ID)
	})
	return moved, err
}

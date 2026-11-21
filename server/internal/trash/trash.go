// Package trash deletes trashed items for good: after 30 days in the trash, or when it's emptied.
//
// Photostead deletes only its own copies: files in writable, managed libraries (the photostead
// share), plus the item's renders. Files in read-only folders it indexes are never touched. The item stays as a record (assets.purged_at), so such a copy can't
// bring it back on the next scan. An item whose file can't be deleted stays in the trash.
package trash

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// After is how long items wait in the trash.
const After = 30 * 24 * time.Hour

// Stats says what a purge did.
type Stats struct {
	Deleted int // items deleted for good
	Files   int // files removed from managed libraries
	Kept    int // items left in the trash because a file couldn't be removed
}

// Purge deletes these trashed items for good.
func Purge(ctx context.Context, s *store.Store, r media.Renderer, ids []uuid.UUID, log *slog.Logger) (Stats, error) {
	var st Stats
	if len(ids) == 0 {
		return st, nil
	}
	files, err := s.Q.WritableFiles(ctx, ids)
	if err != nil {
		return st, err
	}
	failed := map[uuid.UUID]bool{}
	var removed []uuid.UUID
	for _, f := range files {
		root := filepath.Clean(f.RootPath)
		p := filepath.Join(root, filepath.FromSlash(f.RelPath))
		if !strings.HasPrefix(p, root+string(filepath.Separator)) {
			failed[f.AssetID.UUID] = true // never outside the library
			log.Warn("trash: file outside its library left alone", "path", p)
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed[f.AssetID.UUID] = true
			log.Warn("trash: couldn't delete a file; the item stays in the trash", "path", p, "err", err)
			continue
		}
		removed = append(removed, f.ID)
		st.Files++
		// Tidy the folders it leaves empty (the day's, then the month's), never the library itself.
		for dir := filepath.Dir(p); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break
			}
		}
	}
	var done []uuid.UUID
	for _, id := range ids {
		if failed[id] {
			st.Kept++
			continue
		}
		done = append(done, id)
		thumb, preview := r.Paths(id)
		_ = os.Remove(thumb)
		_ = os.Remove(preview)
		_ = os.Remove(r.PlayPath(id))
		for _, crop := range r.FaceCrops(id) {
			_ = os.Remove(crop)
		}
	}
	err = s.Tx(ctx, func(q *db.Queries) error {
		if err := q.MarkPurged(ctx, db.MarkPurgedParams{FileIds: nonNil(removed), Ids: nonNil(done)}); err != nil {
			return err
		}
		return q.ForgetPurged(ctx, nonNil(done))
	})
	if err != nil {
		return st, err
	}
	st.Deleted = len(done)
	return st, nil
}

// Empty deletes the owner's trashed items for good: the ones given (with whatever went into the
// trash with them), or the whole trash when none are given. The vault's trash is apart (vault).
func Empty(ctx context.Context, s *store.Store, r media.Renderer, owner uuid.UUID, ids []uuid.UUID, vault bool, log *slog.Logger) (Stats, error) {
	all, err := s.Q.TrashedOf(ctx, db.TrashedOfParams{Owner: owner, Ids: nonNil(ids), Vault: vault})
	if err != nil {
		return Stats{}, err
	}
	return inBatches(ctx, s, r, all, log)
}

// Expired deletes for good everything trashed more than After ago.
func Expired(ctx context.Context, s *store.Store, r media.Renderer, now time.Time, log *slog.Logger) (Stats, error) {
	var total Stats
	for {
		ids, err := s.Q.TrashedBefore(ctx, db.TrashedBeforeParams{Before: now.Add(-After), Lim: 500})
		if err != nil || len(ids) == 0 {
			return total, err
		}
		st, err := Purge(ctx, s, r, ids, log)
		total.Deleted, total.Files, total.Kept = total.Deleted+st.Deleted, total.Files+st.Files, total.Kept+st.Kept
		if err != nil || st.Deleted == 0 {
			return total, err // nothing more can be deleted now; try again next time
		}
	}
}

func inBatches(ctx context.Context, s *store.Store, r media.Renderer, ids []uuid.UUID, log *slog.Logger) (Stats, error) {
	var total Stats
	for i := 0; i < len(ids); i += 500 {
		st, err := Purge(ctx, s, r, ids[i:min(i+500, len(ids))], log)
		total.Deleted, total.Files, total.Kept = total.Deleted+st.Deleted, total.Files+st.Files, total.Kept+st.Kept
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func nonNil(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

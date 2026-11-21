package jobs

import (
	"context"
	"os"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

type Pruned struct {
	Files int // files that left the index
	Items int // items removed because no copy remains outside the excluded folder
	Kept  int // items that stay because another copy exists elsewhere
}

// PruneFolder takes an excluded folder's files out of the index. Items with a copy elsewhere
// keep that copy (re-dated from the copies that remain, if they were dated by file time); the
// rest are removed along with their previews. Nothing in the folder itself is touched.
func PruneFolder(ctx context.Context, s *store.Store, render media.Renderer, serverZone string, libraryID uuid.UUID, folder string) (Pruned, error) {
	var out Pruned
	var removed []uuid.UUID
	err := s.Tx(ctx, func(q *db.Queries) error {
		gone, err := q.DeleteFilesUnder(ctx, db.DeleteFilesUnderParams{LibraryID: libraryID, Folder: folder})
		if err != nil {
			return err
		}
		out.Files = len(gone)
		seen := map[uuid.UUID]bool{}
		var touched []uuid.UUID
		for _, a := range gone {
			if a.Valid && !seen[a.UUID] {
				seen[a.UUID] = true
				touched = append(touched, a.UUID)
			}
		}
		if len(touched) == 0 {
			return nil
		}
		if err := q.RepointPrimary(ctx, touched); err != nil {
			return err
		}
		zone, err := time.LoadLocation(serverZone)
		if err != nil {
			return err
		}
		if _, err := Redate(ctx, q, touched, zone); err != nil { // from the copies that remain
			return err
		}
		if removed, err = q.DeleteOrphanAssets(ctx, touched); err != nil {
			return err
		}
		out.Items, out.Kept = len(removed), len(touched)-len(removed)
		return nil
	})
	if err != nil {
		return Pruned{}, err
	}
	for _, id := range removed { // previews are only removed once the database change is committed
		thumb, preview := render.Paths(id)
		_ = os.Remove(thumb)
		_ = os.Remove(preview)
	}
	return out, nil
}

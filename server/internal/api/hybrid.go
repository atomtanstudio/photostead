package api

import (
	"sort"

	"github.com/google/uuid"

	"photostead.com/server/internal/store/db"
)

// Hybrid ranking: several ranked lists (by meaning, by prompt words, by file names) merged by
// reciprocal rank fusion, so an item found by more than one way rises, and an exact word match
// in a prompt or a name is never buried under look-alikes.

const rrfK = 60

func fuse(limit int, lists ...[]TimelineAsset) []TimelineAsset {
	score := map[uuid.UUID]float64{}
	first := map[uuid.UUID]TimelineAsset{}
	for _, list := range lists {
		for rank, a := range list {
			score[a.ID] += 1 / float64(rrfK+rank+1)
			if _, ok := first[a.ID]; !ok {
				first[a.ID] = a
			}
		}
	}
	out := make([]TimelineAsset, 0, len(first))
	for _, a := range first {
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if score[out[i].ID] != score[out[j].ID] {
			return score[out[i].ID] > score[out[j].ID]
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// listed turns name or prompt search rows into timeline items.
func listed(rows []db.SearchByNameRow) []TimelineAsset {
	out := make([]TimelineAsset, len(rows))
	for i, r := range rows {
		a := TimelineAsset{ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: r.LocalDate.Time.Format("2006-01-02"),
			Width: r.Width.Int32, Height: r.Height.Int32, DurationMs: r.DurationMs.Int32, ThumbHash: r.Thumbhash, Render: r.RenderState,
			Favorite: r.Favorite, AI: aiKind(r.SourceType)}
		if r.LiveMotionID.Valid {
			id := r.LiveMotionID.UUID
			a.MotionID = &id
		}
		out[i] = a
	}
	return out
}

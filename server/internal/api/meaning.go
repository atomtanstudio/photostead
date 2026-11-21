package api

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/search"
)

const meaningLimit = 300

// nearest finds the items whose embeddings are closest to a query vector, through the gate. The
// HNSW index is searched with iterative scans, so filters (visibility, type) can't starve the
// result; relaxed order is put right after.
const nearestSQL = `
SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state, a.live_motion_id,
       EXISTS (SELECT 1 FROM asset_user_state s WHERE s.asset_id = a.id AND s.user_id = $1 AND s.favorite) AS favorite,
       a.source_type,
       1 - ((e.embedding::halfvec(768)) <=> $2::halfvec(768)) AS score
FROM asset_embeddings e JOIN visible_assets($1) a ON a.id = e.asset_id
WHERE e.model = 'siglip2-base-patch16-256' AND a.visibility = 'timeline' AND a.motion_of IS NULL
  AND ($3 = '' OR a.type = $3) AND a.id <> $4
ORDER BY (e.embedding::halfvec(768)) <=> $2::halfvec(768)
LIMIT $5`

type scored struct {
	TimelineAsset
	score float64
}

func (s *server) nearest(ctx context.Context, vec string, typ string, except uuid.UUID) ([]TimelineAsset, error) {
	var out []scored
	err := pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL hnsw.iterative_scan = relaxed_order; SET LOCAL hnsw.ef_search = 400"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, nearestSQL, viewer(ctx), vec, typ, except, meaningLimit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a scored
			var local pgtype.Date
			var w, h, dur pgtype.Int4
			var motion uuid.NullUUID
			var sourceType string
			if err := rows.Scan(&a.ID, &a.Type, &a.TakenAt, &local, &w, &h, &dur, &a.ThumbHash, &a.Render, &motion, &a.Favorite, &sourceType, &a.score); err != nil {
				return err
			}
			a.LocalDate, a.Width, a.Height, a.DurationMs, a.AI = local.Time.Format("2006-01-02"), w.Int32, h.Int32, dur.Int32, aiKind(sourceType)
			if motion.Valid {
				id := motion.UUID
				a.MotionID = &id
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	assets := make([]TimelineAsset, len(out))
	for i := range out {
		assets[i] = out[i].TimelineAsset
	}
	return assets, nil
}

// itemVector is an item's own embedding (for "More like this"), through the gate.
func (s *server) itemVector(ctx context.Context, id uuid.UUID) (string, error) {
	var v string
	err := s.Store.Pool.QueryRow(ctx, `
		SELECT e.embedding::text FROM asset_embeddings e JOIN visible_assets($1) a ON a.id = e.asset_id
		WHERE e.asset_id = $2 AND e.model = $3`, viewer(ctx), id, search.Model).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

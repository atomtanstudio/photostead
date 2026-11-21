// Package videos makes sure every video plays in every browser. Each video is probed once (its
// codecs, container and HDR); one that isn't plain H.264 in MP4 gets a transcode task, which a
// worker turns into a copy (see media.CopyRecipe). Long and big videos wait for a GPU worker when
// one has been around this week, so the server's own CPU isn't tied up for hours.
package videos

import (
	"context"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// The transcode tasks' models: small ones any worker takes; large ones only GPU workers.
const (
	Small = media.CopyRecipe
	Large = media.CopyRecipe + "-large"
)

// Stats is what one pass did.
type Stats struct {
	Probed, Unreadable int
	Queued             int64
}

// Refresh probes up to limit videos not probed yet, then queues copies for those that need one.
func Refresh(ctx context.Context, s *store.Store, limit int) (Stats, error) {
	var st Stats
	rows, err := s.Q.VideosToProbe(ctx, int32(limit))
	if err != nil {
		return st, err
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		v, err := media.Probe(ctx, filepath.Join(r.RootPath, filepath.FromSlash(r.RelPath)))
		p := db.SaveVideoProbeParams{AssetID: r.ID, Container: v.Container, Codec: v.Codec, Codecs: v.PlayType(), PixFmt: v.PixFmt,
			BitDepth: int16(v.BitDepth), Transfer: v.Transfer, Hdr: v.HDR, Audio: v.Audio, Width: int32(v.Width), Height: int32(v.Height),
			Direct: err == nil && v.Direct()}
		if err != nil {
			if ctx.Err() != nil { // stopping, not a bad file
				return st, ctx.Err()
			}
			p.Error = pgtype.Text{String: err.Error(), Valid: true}
			st.Unreadable++
		}
		if err := s.Q.SaveVideoProbe(ctx, p); err != nil {
			return st, err
		}
		st.Probed++
	}
	gpu, err := s.Q.GPUWorkerSeen(ctx)
	if err != nil {
		return st, err
	}
	st.Queued, err = s.Q.QueueCopies(ctx, db.QueueCopiesParams{Gpu: gpu, Large: Large, Small: Small, Recipe: media.CopyRecipe})
	return st, err
}

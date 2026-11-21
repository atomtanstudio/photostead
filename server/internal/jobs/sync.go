package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"photostead.com/server/internal/store/db"
)

// SyncPruneArgs clears the sync feed's changes older than 30 days, noting how far, so a phone that
// last read before then starts over (see api/sync.go).
type SyncPruneArgs struct{}

func (SyncPruneArgs) Kind() string { return "sync_prune" }
func (SyncPruneArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type SyncPruneWorker struct {
	river.WorkerDefaults[SyncPruneArgs]
	d *Deps
}

func (w *SyncPruneWorker) Work(ctx context.Context, _ *river.Job[SyncPruneArgs]) error {
	return PruneSync(ctx, w.d.Store.Q, time.Now())
}

// NightlySyncPrune runs it at 4:45 local time.
func NightlySyncPrune(zone *time.Location) *river.PeriodicJob {
	return river.NewPeriodicJob(Daily{Hour: 4, Minute: 45, Zone: zone},
		func() (river.JobArgs, *river.InsertOpts) { return SyncPruneArgs{}, nil }, nil)
}

// PruneSync clears changes from before now less 30 days.
func PruneSync(ctx context.Context, q *db.Queries, now time.Time) error {
	through, err := q.PruneSyncChanges(ctx, now.Add(-30*24*time.Hour))
	if err != nil || through == "" {
		return err
	}
	if b, err := q.ServerSetting(ctx, "sync"); err == nil {
		var st struct {
			PrunedThrough string `json:"prunedThrough"`
		}
		_ = json.Unmarshal(b, &st)
		old, _ := strconv.ParseUint(st.PrunedThrough, 10, 64)
		if n, _ := strconv.ParseUint(through, 10, 64); n < old {
			return nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	b, _ := json.Marshal(map[string]string{"prunedThrough": through})
	return q.SetServerSetting(ctx, db.SetServerSettingParams{Key: "sync", Value: b})
}

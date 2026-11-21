package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/trash"
)

// PurgeTrashArgs deletes for good what has been in the trash for 30 days: every night.
type PurgeTrashArgs struct{}

func (PurgeTrashArgs) Kind() string { return "purge_trash" }
func (PurgeTrashArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type PurgeTrashWorker struct {
	river.WorkerDefaults[PurgeTrashArgs]
	d *Deps
}

func (w *PurgeTrashWorker) Timeout(*river.Job[PurgeTrashArgs]) time.Duration { return time.Hour }

func (w *PurgeTrashWorker) Work(ctx context.Context, _ *river.Job[PurgeTrashArgs]) error {
	st, err := trash.Expired(ctx, w.d.Store, w.d.Render, time.Now(), w.d.Log)
	if st.Deleted > 0 || st.Kept > 0 {
		w.d.Log.Info("trash: deleted items 30 days after they were trashed", "items", st.Deleted, "files", st.Files, "kept", st.Kept)
	}
	return err
}

// NightlyPurge runs the clean-up at 5:00 local time.
func NightlyPurge(zone *time.Location) *river.PeriodicJob {
	return river.NewPeriodicJob(Daily{Hour: 5, Minute: 0, Zone: zone},
		func() (river.JobArgs, *river.InsertOpts) { return PurgeTrashArgs{}, nil }, nil)
}

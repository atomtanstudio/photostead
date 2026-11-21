package jobs

import (
	"context"
	"os"
	"time"

	"github.com/riverqueue/river"
)

// UploadCleanupArgs removes resumable uploads past their three days, with what arrived of them.
type UploadCleanupArgs struct{}

func (UploadCleanupArgs) Kind() string { return "upload_cleanup" }
func (UploadCleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type UploadCleanupWorker struct {
	river.WorkerDefaults[UploadCleanupArgs]
	d *Deps
}

func (w *UploadCleanupWorker) Work(ctx context.Context, _ *river.Job[UploadCleanupArgs]) error {
	gone, err := w.d.Store.Q.ExpiredUploadSessions(ctx)
	for _, g := range gone {
		if !g.Completed {
			_ = os.Remove(g.Path)
		}
	}
	if len(gone) > 0 {
		w.d.Log.Info("removed resumable uploads that ran out", "uploads", len(gone))
	}
	return err
}

// PeriodicUploadCleanup runs it every hour.
func PeriodicUploadCleanup() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return UploadCleanupArgs{}, nil }, nil)
}

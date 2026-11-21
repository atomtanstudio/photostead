package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/backup"
)

// BackupArgs copies the database (see package backup): every night, and when an admin asks.
type BackupArgs struct{}

func (BackupArgs) Kind() string { return "backup" }
func (BackupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type BackupWorker struct {
	river.WorkerDefaults[BackupArgs]
	d *Deps
}

func (w *BackupWorker) Timeout(*river.Job[BackupArgs]) time.Duration { return time.Hour }

func (w *BackupWorker) Work(ctx context.Context, _ *river.Job[BackupArgs]) error {
	if w.d.Backup.Dir == "" {
		return nil
	}
	start := time.Now()
	c, err := backup.Run(ctx, w.d.Backup, start)
	if rerr := backup.Record(ctx, w.d.Store.Q, c, time.Since(start), err); rerr != nil {
		w.d.Log.Warn("backup: recording how it went", "err", rerr)
	}
	if err != nil {
		w.d.Log.Error("backup of the database failed", "err", err)
		return err
	}
	w.d.Log.Info("backed up the database", "copy", c.Name, "bytes", c.Bytes, "took", time.Since(start).Round(time.Second))
	if err := backup.WriteManifest(ctx, w.d.Store.Q, w.d.Backup.Dir, c.Name); err != nil { // the copy stands without it
		w.d.Log.Warn("backup: writing the list of files", "err", err)
	}
	return nil
}

// NightlyBackup copies the database at 3:30 local time, before the nightly rescan and clean-up.
func NightlyBackup(zone *time.Location) *river.PeriodicJob {
	return river.NewPeriodicJob(Daily{Hour: 3, Minute: 30, Zone: zone},
		func() (river.JobArgs, *river.InsertOpts) { return BackupArgs{}, nil }, nil)
}

package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"photostead.com/server/internal/libraries"
)

// DiscoverArgs sets up where photos live on a new install: each person's folder in the photostead
// share, and a read-only library for every folder mounted under /libraries (then scanned).
type DiscoverArgs struct{}

func (DiscoverArgs) Kind() string { return "discover_libraries" }
func (DiscoverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type DiscoverWorker struct {
	river.WorkerDefaults[DiscoverArgs]
	d *Deps
}

func (w *DiscoverWorker) Work(ctx context.Context, _ *river.Job[DiscoverArgs]) error {
	f, err := libraries.Discover(ctx, w.d.Store.Q, w.d.ManagedRoot, w.d.Mounts)
	if f.Managed > 0 {
		w.d.Log.Info("libraries: folders in the photostead share set up", "people", f.Managed)
	}
	client := river.ClientFromContext[pgx.Tx](ctx)
	for _, l := range f.Indexed {
		w.d.Log.Info("libraries: found a mounted folder; indexing it, read-only", "name", l.Name, "path", l.RootPath)
		if _, err := client.Insert(ctx, ScanLibraryArgs{LibraryID: l.ID}, nil); err != nil {
			return err
		}
	}
	return err
}

// PeriodicDiscover looks for new people and mounted folders every 10 minutes, and at start-up.
func PeriodicDiscover() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return DiscoverArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

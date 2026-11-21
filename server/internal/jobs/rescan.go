package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// RescanArgs rescans every watched library: the safety net for changes the folder watcher
// can't see (a file written straight to a disk path behind a share, or a burst of changes too
// big to follow one by one).
type RescanArgs struct{}

func (RescanArgs) Kind() string { return "rescan_watched" }

type RescanWorker struct {
	river.WorkerDefaults[RescanArgs]
	d *Deps
}

func (w *RescanWorker) Work(ctx context.Context, _ *river.Job[RescanArgs]) error {
	libs, err := w.d.Store.Q.WatchedLibraries(ctx)
	if err != nil {
		return err
	}
	client := river.ClientFromContext[pgx.Tx](ctx)
	for _, l := range libs {
		if _, err := client.Insert(ctx, ScanLibraryArgs{LibraryID: l.ID}, nil); err != nil {
			return err
		}
	}
	return nil
}

// Daily is a schedule that fires once a day at a local time.
type Daily struct {
	Hour, Minute int
	Zone         *time.Location
}

func (d Daily) Next(t time.Time) time.Time {
	lt := t.In(d.Zone)
	next := time.Date(lt.Year(), lt.Month(), lt.Day(), d.Hour, d.Minute, 0, 0, d.Zone)
	if !next.After(t) {
		next = time.Date(lt.Year(), lt.Month(), lt.Day()+1, d.Hour, d.Minute, 0, 0, d.Zone)
	}
	return next
}

// NightlyRescan rescans the watched libraries at 4:30 every morning, in the server's zone:
// after Unraid's usual mover time, before anyone is up.
func NightlyRescan(zone *time.Location) *river.PeriodicJob {
	return river.NewPeriodicJob(Daily{Hour: 4, Minute: 30, Zone: zone},
		func() (river.JobArgs, *river.InsertOpts) { return RescanArgs{}, nil }, nil)
}

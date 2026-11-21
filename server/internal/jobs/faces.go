package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/faces"
)

// FacesArgs groups owners' faces into people when there's something new: faces found since the
// last run, or a correction. Every 20 minutes, and right after a correction.
type FacesArgs struct{}

func (FacesArgs) Kind() string { return "group_faces" }
func (FacesArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type FacesWorker struct {
	river.WorkerDefaults[FacesArgs]
	d *Deps
}

func (w *FacesWorker) Timeout(*river.Job[FacesArgs]) time.Duration { return 3 * time.Hour }

func (w *FacesWorker) Work(ctx context.Context, _ *river.Job[FacesArgs]) error {
	owners, err := w.d.Store.Q.FaceOwners(ctx)
	if err != nil {
		return err
	}
	for _, o := range owners {
		due, err := w.d.Store.Q.FaceRunDue(ctx, o)
		if err != nil || !due {
			if err != nil {
				return err
			}
			continue
		}
		if _, err := faces.Run(ctx, w.d.Store, o, w.d.Log); err != nil {
			return err
		}
	}
	return nil
}

// PeriodicFaces groups new faces every 20 minutes, and once at start-up.
func PeriodicFaces() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(20*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return FacesArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/places"
)

// PlacesArgs names the places of items with a location (offline, from GeoNames): every 30 minutes.
type PlacesArgs struct{}

func (PlacesArgs) Kind() string { return "places" }
func (PlacesArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type PlacesWorker struct {
	river.WorkerDefaults[PlacesArgs]
	d *Deps
}

func (w *PlacesWorker) Timeout(*river.Job[PlacesArgs]) time.Duration { return 30 * time.Minute }

func (w *PlacesWorker) Work(ctx context.Context, _ *river.Job[PlacesArgs]) error {
	st, err := places.Refresh(ctx, w.d.Store, w.d.Render.DataDir)
	if st.Located > 0 {
		w.d.Log.Info("places", "items with a location", st.Located, "named", st.Placed)
	}
	return err
}

// PeriodicPlaces names new items' places every 30 minutes, and once at start-up.
func PeriodicPlaces() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(30*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return PlacesArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

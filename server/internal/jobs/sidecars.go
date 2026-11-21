package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/sidecar"
)

// SidecarsArgs writes the XMP sidecars of items whose marks, keywords, description, date, people or
// edit changed: every 5 minutes.
type SidecarsArgs struct{}

func (SidecarsArgs) Kind() string { return "sidecars" }
func (SidecarsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type SidecarsWorker struct {
	river.WorkerDefaults[SidecarsArgs]
	d *Deps
}

func (w *SidecarsWorker) Timeout(*river.Job[SidecarsArgs]) time.Duration { return 30 * time.Minute }

func (w *SidecarsWorker) Work(ctx context.Context, _ *river.Job[SidecarsArgs]) error {
	var total sidecar.Stats
	defer func() {
		if total.Written > 0 || total.Failed > 0 {
			w.d.Log.Info("sidecars", "written", total.Written, "unchanged", total.Unchanged, "failed", total.Failed)
		}
	}()
	for deadline := time.Now().Add(20 * time.Minute); time.Now().Before(deadline); {
		st, err := sidecar.Write(ctx, w.d.Store, w.d.Render.DataDir, 500)
		total.Written, total.Unchanged, total.Skipped, total.Failed = total.Written+st.Written, total.Unchanged+st.Unchanged, total.Skipped+st.Skipped, total.Failed+st.Failed
		if err != nil || st.Written+st.Unchanged+st.Skipped+st.Failed == 0 {
			return err
		}
	}
	return nil
}

// PeriodicSidecars writes changed items' sidecars every 5 minutes, and once at start-up.
func PeriodicSidecars() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return SidecarsArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

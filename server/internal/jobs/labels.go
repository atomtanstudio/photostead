package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/labels"
)

// LabelsArgs sorts items into the utility collections (screenshots, documents, receipts, ID
// cards, QR codes): every 15 minutes, once their search embedding and text are in.
type LabelsArgs struct{}

func (LabelsArgs) Kind() string { return "labels" }
func (LabelsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type LabelsWorker struct {
	river.WorkerDefaults[LabelsArgs]
	d *Deps
}

func (w *LabelsWorker) Timeout(*river.Job[LabelsArgs]) time.Duration { return 30 * time.Minute }

func (w *LabelsWorker) Work(ctx context.Context, _ *river.Job[LabelsArgs]) error {
	if w.d.Embedder == nil {
		return nil
	}
	st, err := labels.Refresh(ctx, w.d.Store, w.d.Embedder)
	if st.Checked > 0 {
		w.d.Log.Info("utility labels", "items looked at", st.Checked, "labels", st.Labeled)
	}
	return err
}

// PeriodicLabels labels new items every 15 minutes, and once at start-up.
func PeriodicLabels() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return LabelsArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

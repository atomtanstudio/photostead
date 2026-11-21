package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"
)

// ShareRulesArgs applies everyone's auto-share rules: new items that match go into the Household
// space. Every 10 minutes, and once at start-up (a rule's own changes apply at once, in the API).
type ShareRulesArgs struct{}

func (ShareRulesArgs) Kind() string { return "share_rules" }
func (ShareRulesArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type ShareRulesWorker struct {
	river.WorkerDefaults[ShareRulesArgs]
	d *Deps
}

func (w *ShareRulesWorker) Work(ctx context.Context, _ *river.Job[ShareRulesArgs]) error {
	n, err := w.d.Store.Q.ApplyShareRules(ctx, nil)
	if err == nil && n > 0 {
		w.d.Log.Info("household: shared by auto-share rules", "items", n)
	}
	return err
}

func PeriodicShareRules() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return ShareRulesArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

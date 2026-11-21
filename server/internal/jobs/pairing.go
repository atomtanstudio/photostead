package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/stacks"
	"photostead.com/server/internal/store/db"
)

// Pairing reports what a pairing pass linked.
type Pairing struct {
	LivePhotosByIdentifier int64
	LivePhotosByName       int64
	RawJpegStacks          int64
}

// Pair links what belongs together: Live Photo stills with their motion clips (by Apple's
// identifier, then by name for exports without one) and RAW+JPEG pairs (as stacks). It only adds
// links that don't exist yet, so it's safe to run any time.
func Pair(ctx context.Context, q *db.Queries) (Pairing, error) {
	var p Pairing
	var err error
	if p.LivePhotosByIdentifier, err = q.PairLivePhotosByIdentifier(ctx); err != nil {
		return p, err
	}
	if p.LivePhotosByName, err = q.PairLivePhotosByName(ctx); err != nil {
		return p, err
	}
	if p.RawJpegStacks, err = q.StackRawJpegPairs(ctx); err != nil {
		return p, err
	}
	return p, nil
}

type PairArgs struct{}

func (PairArgs) Kind() string { return "pair_assets" }
func (PairArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

// PairWorker runs a pairing pass. It's scheduled periodically, so pairs are found once both
// halves have been indexed, whichever arrives last.
type PairWorker struct {
	river.WorkerDefaults[PairArgs]
	d *Deps
}

func (w *PairWorker) Timeout(*river.Job[PairArgs]) time.Duration { return 10 * time.Minute }

func (w *PairWorker) Work(ctx context.Context, _ *river.Job[PairArgs]) error {
	p, err := Pair(ctx, w.d.Store.Q)
	if err == nil && p.LivePhotosByIdentifier+p.LivePhotosByName+p.RawJpegStacks > 0 {
		w.d.Log.Info("paired", "live photos (identifier)", p.LivePhotosByIdentifier, "live photos (name)", p.LivePhotosByName, "raw+jpeg stacks", p.RawJpegStacks)
	}
	if err != nil {
		return err
	}
	_, err = stacks.Run(ctx, w.d.Store, w.d.Log) // AI batches and bursts
	return err
}

// LookAlikesArgs compares items not compared before with their nearest look-alikes, and suggests
// stacking near-duplicates.
type LookAlikesArgs struct{}

func (LookAlikesArgs) Kind() string { return "look_alikes" }
func (LookAlikesArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type LookAlikesWorker struct {
	river.WorkerDefaults[LookAlikesArgs]
	d *Deps
}

func (w *LookAlikesWorker) Timeout(*river.Job[LookAlikesArgs]) time.Duration { return time.Hour }

func (w *LookAlikesWorker) Work(ctx context.Context, _ *river.Job[LookAlikesArgs]) error {
	for { // in rounds, so a first pass over a big library saves as it goes
		st, err := stacks.NearDuplicates(ctx, w.d.Store, 5000, w.d.Log)
		if err != nil || st.Checked < 5000 {
			return err
		}
	}
}

// PeriodicLookAlikes compares new items every 30 minutes, and once at start-up.
func PeriodicLookAlikes() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(30*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return LookAlikesArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

// PeriodicPairing schedules a pairing pass every 15 minutes, and one at start-up.
func PeriodicPairing() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return PairArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"photostead.com/server/internal/characters"
)

// CharactersArgs recomputes who's in what for every owner's characters, and which image each clip
// was made from: after AI details are read, and every 15 minutes so new files join their characters.
type CharactersArgs struct{}

func (CharactersArgs) Kind() string { return "characters" }
func (CharactersArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type CharactersWorker struct {
	river.WorkerDefaults[CharactersArgs]
	d *Deps
}

func (w *CharactersWorker) Timeout(*river.Job[CharactersArgs]) time.Duration { return 10 * time.Minute }

func (w *CharactersWorker) Work(ctx context.Context, _ *river.Job[CharactersArgs]) error {
	owners, err := w.d.Store.Q.CharacterOwners(ctx)
	if err != nil {
		return err
	}
	for _, o := range owners {
		st, err := characters.Refresh(ctx, w.d.Store, o)
		if err != nil {
			return err
		}
		w.d.Log.Info("characters refreshed", "characters", st.Characters, "from LoRAs", st.Metadata, "videos from their images", st.Lineage,
			"start images", st.Starts)
	}
	return nil
}

// PeriodicCharacters refreshes characters every 15 minutes, and once at start-up.
func PeriodicCharacters() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return CharactersArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"photostead.com/server/internal/immich"
	"photostead.com/server/internal/store/db"
)

// ImmichImportArgs imports an owner's favorites, archive, albums and people's names from the
// Immich server they connected.
type ImmichImportArgs struct {
	OwnerID uuid.UUID `json:"owner_id"`
	RunID   uuid.UUID `json:"run_id"` // the record made when it was asked for
	DryRun  bool      `json:"dry_run,omitempty"`
}

func (ImmichImportArgs) Kind() string { return "immich_import" }

// A second request while one is queued or running is the same import. Two attempts, so a run
// a restart cut short starts again on its own.
func (ImmichImportArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 2, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: whileQueued.ByState}}
}

type ImmichImportWorker struct {
	river.WorkerDefaults[ImmichImportArgs]
	d *Deps
}

func (w *ImmichImportWorker) Timeout(*river.Job[ImmichImportArgs]) time.Duration { return time.Hour }

func (w *ImmichImportWorker) Work(ctx context.Context, job *river.Job[ImmichImportArgs]) error {
	c, err := w.d.Store.Q.GetConnection(ctx, db.GetConnectionParams{OwnerID: job.Args.OwnerID, Kind: "immich"})
	if errors.Is(err, pgx.ErrNoRows) { // forgotten since it was asked for
		_ = w.d.Store.Q.FinishImport(ctx, db.FinishImportParams{ID: job.Args.RunID, State: "failed",
			Error: pgtype.Text{String: "No Immich server is connected", Valid: true}})
		return river.JobCancel(errors.New("no Immich server is connected"))
	} else if err != nil {
		return err
	}
	im := &immich.Importer{Store: w.d.Store, Client: &immich.Client{URL: c.Url, Key: c.Secret}, Log: w.d.Log, DryRun: job.Args.DryRun}
	sum, err := immich.Record(ctx, im, job.Args.OwnerID, job.Args.RunID)
	if err != nil {
		return err
	}
	w.d.Log.Info("immich import finished", "matched", sum.Matched, "of", sum.Items, "favorites", sum.Favorites,
		"archived", sum.Archived, "albums", sum.Albums, "people", sum.People, "dry run", sum.DryRun)
	return nil
}

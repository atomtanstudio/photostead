package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"photostead.com/server/internal/refile"
)

// RefileArgs moves an owner's managed files to their items' new day folders after a date change.
type RefileArgs struct {
	OwnerID  uuid.UUID   `json:"owner_id"`
	AssetIDs []uuid.UUID `json:"asset_ids"`
}

func (RefileArgs) Kind() string                 { return "refile" }
func (RefileArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

type RefileWorker struct {
	river.WorkerDefaults[RefileArgs]
	d *Deps
}

func (w *RefileWorker) Timeout(*river.Job[RefileArgs]) time.Duration { return time.Hour }

func (w *RefileWorker) Work(ctx context.Context, job *river.Job[RefileArgs]) error {
	_, err := refile.Run(ctx, w.d.Store, job.Args.OwnerID, job.Args.AssetIDs, w.d.Log)
	return err
}

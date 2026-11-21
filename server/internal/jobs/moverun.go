package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"photostead.com/server/internal/move"
	"photostead.com/server/internal/store/db"
)

// MoveRunArgs carries out a reviewed move plan, a chunk at a time. Limit caps the total number
// of files (0 means all), so a pilot run can copy a handful first.
type MoveRunArgs struct {
	PlanID uuid.UUID `json:"plan_id"`
	Limit  int       `json:"limit"`
}

func (MoveRunArgs) Kind() string { return "move_run" }

// Move jobs have a queue of their own that runs one job at a time, so runs never overlap; a
// duplicate request just finds nothing left to do.
func (MoveRunArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueMove, MaxAttempts: 10}
}

const (
	QueueMove = "move"
	// A chunk stops after about this much, so each job finishes well inside the queue's
	// stuck-job window (an hour) even at modest disk speeds.
	moveChunkFiles = 2000
	moveChunkBytes = 20 << 30
)

type MoveRunWorker struct {
	river.WorkerDefaults[MoveRunArgs]
	d *Deps
}

func (w *MoveRunWorker) Timeout(*river.Job[MoveRunArgs]) time.Duration { return 50 * time.Minute }

func (w *MoveRunWorker) Work(ctx context.Context, job *river.Job[MoveRunArgs]) error {
	chunk := moveChunkFiles
	if job.Args.Limit > 0 {
		chunk = min(chunk, job.Args.Limit)
	}
	c := move.Copier{Store: w.d.Store, Target: w.d.MoveTarget, ManagedRoot: w.d.ManagedRoot, Workers: 2, Log: w.d.Log}
	p, err := c.Run(ctx, job.Args.PlanID, chunk, moveChunkBytes)
	if err != nil {
		return err
	}
	w.d.Log.Info("move: chunk finished", "plan", job.Args.PlanID, "copied", p.Done, "failed", p.Failed, "bytes", p.Bytes)
	left := job.Args.Limit
	if left > 0 {
		left -= p.Done + p.Failed
		if left <= 0 {
			return nil
		}
	}
	if p.Done+p.Failed < chunk && p.Bytes < moveChunkBytes { // nothing more pending
		return nil
	}
	// More to do: continue in a fresh job, so a restart never repeats more than one chunk.
	_, err = river.ClientFromContext[pgx.Tx](ctx).Insert(ctx, MoveRunArgs{PlanID: job.Args.PlanID, Limit: left}, nil)
	return err
}

// MoveStatus summarizes a plan's progress.
func MoveStatus(ctx context.Context, q *db.Queries, planID uuid.UUID) ([]db.MoveProgressRow, []db.MoveFailuresRow, error) {
	prog, err := q.MoveProgress(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	fails, err := q.MoveFailures(ctx, db.MoveFailuresParams{PlanID: planID, Lim: 20})
	return prog, fails, err
}

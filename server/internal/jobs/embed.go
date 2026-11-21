package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"photostead.com/server/internal/faces"
	"photostead.com/server/internal/labels"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// EmbedQueueArgs queues search embeddings, faces, pets, adult content and text reading (OCR) for items that don't have them
// yet: every 10 minutes, so new items become searchable soon after they're rendered.
type EmbedQueueArgs struct{}

func (EmbedQueueArgs) Kind() string { return "embed_queue" }
func (EmbedQueueArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type EmbedQueueWorker struct {
	river.WorkerDefaults[EmbedQueueArgs]
	d *Deps
}

func (w *EmbedQueueWorker) Work(ctx context.Context, _ *river.Job[EmbedQueueArgs]) error {
	n, err := w.d.Store.Q.QueueEmbeddings(ctx, search.Model)
	if err != nil {
		return err
	}
	if n > 0 {
		w.d.Log.Info("search: queued items to embed", "items", n)
	}
	n, err = w.d.Store.Q.QueueOCR(ctx, search.OCREngine)
	if err != nil {
		return err
	}
	if n > 0 {
		w.d.Log.Info("search: queued items to read text from", "items", n)
	}
	n, err = w.d.Store.Q.QueueFaces(ctx, faces.Engine)
	if err == nil && n > 0 {
		w.d.Log.Info("faces: queued items to look at", "items", n)
	}
	if err != nil {
		return err
	}
	n, err = w.d.Store.Q.QueuePets(ctx, faces.PetEngine)
	if err == nil && n > 0 {
		w.d.Log.Info("pets: queued items to look at", "items", n)
	}
	if err != nil {
		return err
	}
	n, err = w.d.Store.Q.QueueAdult(ctx, labels.AdultEngine)
	if err == nil && n > 0 {
		w.d.Log.Info("adult content: queued items to look at", "items", n)
	}
	if err != nil {
		return err
	}
	relabelled, err := RelabelAdult(ctx, w.d.Store)
	if relabelled > 0 {
		w.d.Log.Info("adult content: labelled again by the current rule", "items", relabelled)
	}
	return err
}

func (w *EmbedQueueWorker) Timeout(*river.Job[EmbedQueueArgs]) time.Duration { return 10 * time.Minute }

// RelabelAdult labels again, from the probabilities kept, every item a scan labelled by an older
// rule (labels.AdultRule): a new rule needs no new scan. The owner's corrections stay.
func RelabelAdult(ctx context.Context, s *store.Store) (int, error) {
	total := 0
	for {
		rows, err := s.Q.AdultToRelabel(ctx, db.AdultToRelabelParams{Rule: labels.AdultRule, Lim: 2000})
		if err != nil || len(rows) == 0 {
			return total, err
		}
		ids := make([]uuid.UUID, len(rows))
		var add db.AddScanLabelsParams
		for i, r := range rows {
			ids[i] = r.AssetID
			if len(r.Scores) == 0 || len(r.Scores)%4 != 0 {
				continue
			}
			if l, score := labels.AdultOf(r.Scores); l != "" {
				add.Ids, add.Labels, add.Scores = append(add.Ids, r.AssetID), append(add.Labels, l), append(add.Scores, score)
			}
		}
		err = s.Tx(ctx, func(q *db.Queries) error {
			if err := q.ClearScanLabelsAmong(ctx, ids); err != nil {
				return err
			}
			if len(add.Ids) > 0 {
				if err := q.AddScanLabels(ctx, add); err != nil {
					return err
				}
			}
			return q.SetAdultRule(ctx, db.SetAdultRuleParams{Rule: labels.AdultRule, Ids: ids})
		})
		if err != nil {
			return total, err
		}
		total += len(rows)
	}
}

func PeriodicEmbedQueue() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return EmbedQueueArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}

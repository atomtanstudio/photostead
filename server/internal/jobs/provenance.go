package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"photostead.com/server/internal/provenance"
	"photostead.com/server/internal/store/db"
)

// QueueProvenance reads files again for AI details the index cut short. One job at a time, so it
// never competes much with anything else for the disks; pause it (queue pause provenance) to
// hold it back.
const QueueProvenance = "provenance"

// ProvenanceArgs reads AI provenance from the indexed metadata of every item the current parser
// hasn't read yet. It reads no files, so it's cheap; it runs at every start and after a parser
// upgrade.
type ProvenanceArgs struct{}

func (ProvenanceArgs) Kind() string { return "provenance" }
func (ProvenanceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: whileQueued}
}

type ProvenanceWorker struct {
	river.WorkerDefaults[ProvenanceArgs]
	d *Deps
}

func (w *ProvenanceWorker) Timeout(*river.Job[ProvenanceArgs]) time.Duration { return time.Hour }

func (w *ProvenanceWorker) Work(ctx context.Context, _ *river.Job[ProvenanceArgs]) error {
	q := w.d.Store.Q
	var after uuid.UUID
	var read, ai, needsFile int
	for {
		rows, err := q.ProvenancePending(ctx, db.ProvenancePendingParams{Version: pgtype.Int2{Int16: provenance.Version, Valid: true}, After: after, Lim: 500})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			after = r.ID
			var tags provenance.Tags
			if err := json.Unmarshal(r.Raw, &tags); err != nil {
				tags = provenance.Tags{}
			}
			g, nf := provenance.Parse(tags, r.Type == "video")
			if err := provenance.Save(ctx, w.d.Store, r.ID, r.OwnerID, g, nf); err != nil {
				return err
			}
			read++
			if g != nil {
				ai++
			}
			if nf {
				needsFile++
			}
		}
		if read%10000 < 500 {
			w.d.Log.Info("provenance: reading", "items", read, "ai", ai)
		}
	}
	if read > 0 {
		w.d.Log.Info("provenance: done", "items read", read, "with AI evidence", ai, "to read from the file", needsFile)
		_, err := river.ClientFromContext[pgx.Tx](ctx).Insert(ctx, CharactersArgs{}, nil) // new LoRAs may make new members
		return err
	}
	return nil
}

// ProvenanceFilesArgs reads the files of items whose AI details were cut short in the index, a
// chunk at a time; each chunk queues the next while any are left.
type ProvenanceFilesArgs struct{}

func (ProvenanceFilesArgs) Kind() string { return "provenance_files" }
func (ProvenanceFilesArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueProvenance, MaxAttempts: 5}
}

type ProvenanceFilesWorker struct {
	river.WorkerDefaults[ProvenanceFilesArgs]
	d *Deps
}

func (w *ProvenanceFilesWorker) Timeout(*river.Job[ProvenanceFilesArgs]) time.Duration {
	return 50 * time.Minute
}

const provenanceChunk = 1000

func (w *ProvenanceFilesWorker) Work(ctx context.Context, _ *river.Job[ProvenanceFilesArgs]) error {
	d, q := w.d, w.d.Store.Q
	rows, err := q.ProvenanceNeedsFile(ctx, db.ProvenanceNeedsFileParams{After: uuid.Nil, Lim: provenanceChunk})
	if err != nil || len(rows) == 0 {
		return err
	}
	var mu sync.Mutex
	var read, failed int
	work := make(chan db.ProvenanceNeedsFileRow)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range work {
				err := d.readProvenance(ctx, r.ID, r.OwnerID, r.Type, r.RootPath, r.RelPath)
				mu.Lock()
				if err != nil {
					failed++
					d.Log.Warn("provenance: couldn't read the file", "file", r.RelPath, "err", err)
				} else {
					read++
				}
				mu.Unlock()
			}
		}()
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			break
		}
		work <- r
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	d.Log.Info("provenance: files read", "read", read, "failed", failed)
	client := river.ClientFromContext[pgx.Tx](ctx)
	if len(rows) == provenanceChunk && read > 0 {
		_, err := client.Insert(ctx, ProvenanceFilesArgs{}, nil)
		return err
	}
	_, err = client.Insert(ctx, CharactersArgs{}, nil) // all read: LoRAs and lineage are complete now
	return err
}

// readProvenance reads an item's file with exiftool and records its provenance from the whole of
// its metadata. A file that can't be read is marked done, so it isn't tried forever.
func (d *Deps) readProvenance(ctx context.Context, id, owner uuid.UUID, kind, root, rel string) error {
	path, err := within(root, rel)
	if err == nil {
		var tags map[string]any
		if tags, err = d.Exif.Read(ctx, path); err == nil {
			g, _ := provenance.Parse(tags, kind == "video")
			return provenance.Save(ctx, d.Store, id, owner, g, false)
		}
	}
	if ctx.Err() == nil {
		_ = d.Store.Q.SetProvenanceNeedsFile(ctx, db.SetProvenanceNeedsFileParams{ID: id, NeedsFile: false})
	}
	return err
}

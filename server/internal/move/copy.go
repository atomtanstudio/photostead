package move

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Copier carries out a reviewed plan. Originals are only ever read; each copy is verified by
// re-reading it before anything points at it, and nothing is ever overwritten.
type Copier struct {
	Store *store.Store
	// Target is where the managed share is written. On Unraid the one-time migration writes
	// through the array-only view (/mnt/user0/photostead), so terabytes never land on the cache.
	Target string
	// ManagedRoot is where the app reads the managed share (/mnt/user/photostead): the files
	// are recorded under it.
	ManagedRoot string
	Workers     int
	Log         *slog.Logger
}

type Progress struct {
	Done, Failed int
	Bytes        int64
}

const partialSuffix = ".photostead-partial"

// Run copies pending entries of a plan until limit files (0: no limit) or about maxBytes
// (0: no limit) have been handled, and returns what it did. It can be stopped and run again at
// any time; finished entries are skipped. Runs of one plan must not overlap (the job queue runs
// them one at a time).
func (c Copier) Run(ctx context.Context, planID uuid.UUID, limit int, maxBytes int64) (Progress, error) {
	var p Progress
	q := c.Store.Q
	plan, err := q.GetMovePlan(ctx, planID)
	if err != nil {
		return p, fmt.Errorf("move plan %s: %w", planID, err)
	}
	if info, err := os.Stat(c.Target); err != nil || !info.IsDir() {
		return p, fmt.Errorf("the destination %s isn't available (is the photostead share mounted?)", c.Target)
	}
	managedRoot := filepath.Join(c.ManagedRoot, "library", plan.Username)
	libID, err := q.EnsureManagedLibrary(ctx, db.EnsureManagedLibraryParams{OwnerID: plan.OwnerID, RootPath: managedRoot})
	if err != nil {
		return p, err
	}
	prefix := "library/" + plan.Username + "/"
	workers := max(1, c.Workers)
	for (limit == 0 || p.Done+p.Failed < limit) && (maxBytes == 0 || p.Bytes < maxBytes) {
		batch := int32(20) // small batches, so the byte cap is checked often
		if limit > 0 {
			batch = int32(min(20, limit-p.Done-p.Failed))
		}
		entries, err := q.PendingMoveEntries(ctx, db.PendingMoveEntriesParams{PlanID: planID, Batch: batch})
		if err != nil {
			return p, err
		}
		if len(entries) == 0 {
			break
		}
		var mu sync.Mutex
		var firstErr error
		work := make(chan db.PendingMoveEntriesRow)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for e := range work {
					size, err := c.one(ctx, e, libID, prefix, planID)
					mu.Lock()
					switch {
					case err == nil:
						p.Done++
						p.Bytes += size
					case errors.Is(err, errStop):
						if firstErr == nil {
							firstErr = err
						}
					default:
						p.Failed++
					}
					mu.Unlock()
				}
			}()
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				break
			}
			work <- e
		}
		close(work)
		wg.Wait()
		if firstErr != nil {
			return p, firstErr
		}
		if ctx.Err() != nil {
			return p, ctx.Err()
		}
	}
	return p, nil
}

// errStop marks problems that should stop the run (the database is unreachable) rather than
// fail one file.
var errStop = errors.New("stopping the move")

// one copies and verifies a single entry and records the outcome. A returned error that isn't
// errStop has already been recorded against the entry.
func (c Copier) one(ctx context.Context, e db.PendingMoveEntriesRow, libID uuid.UUID, prefix string, planID uuid.UUID) (int64, error) {
	fail := func(reason string) (int64, error) {
		if err := c.Store.Q.FinishMoveEntry(ctx, db.FinishMoveEntryParams{PlanID: planID, DstRel: e.DstRel, State: "failed",
			Error: pgtype.Text{String: reason, Valid: true}}); err != nil {
			return 0, fmt.Errorf("%w: %v", errStop, err)
		}
		if c.Log != nil {
			c.Log.Warn("move: copy failed", "file", e.SrcRel, "reason", reason)
		}
		return 0, errors.New(reason)
	}
	src, err := within(e.RootPath, e.SrcRel)
	if err != nil {
		return fail(err.Error())
	}
	dst, err := within(c.Target, e.DstRel)
	if err != nil {
		return fail(err.Error())
	}
	info, err := os.Stat(src)
	if err != nil {
		return fail("the original is missing: " + err.Error())
	}
	mtime := info.ModTime()

	sum, size, err := copyVerified(ctx, src, dst, mtime)
	switch {
	case ctx.Err() != nil: // stopped mid-file: the entry stays pending for the next run
		return 0, fmt.Errorf("%w: %v", errStop, ctx.Err())
	case errors.Is(err, errDifferentAtDestination):
		return fail("a different file already exists at the destination; nothing was overwritten")
	case err != nil:
		return fail(err.Error())
	}
	if e.SrcSha256 != nil && !bytes.Equal(sum, e.SrcSha256) {
		// The original changed after it was indexed; the copy matches today's bytes, but the plan
		// was made for the old ones. Take the copy back out and leave it for a new plan.
		_ = os.Remove(dst)
		return fail("the original changed since it was indexed; make a new plan")
	}

	kind := "sidecar"
	if e.Role != "sidecar" && e.SrcKind.Valid {
		kind = e.SrcKind.String
	}
	err = c.Store.Tx(ctx, func(q *db.Queries) error {
		fid, err := q.RecordMovedFile(ctx, db.RecordMovedFileParams{LibraryID: libID, RelPath: strings.TrimPrefix(e.DstRel, prefix),
			Size: size, Mtime: mtime, Sha256: sum, Kind: kind, Role: e.Role, AssetID: e.AssetID})
		if err != nil {
			return err
		}
		if e.Role == "original" && e.AssetID.Valid {
			if err := q.UseMovedFile(ctx, db.UseMovedFileParams{FileID: uuid.NullUUID{UUID: fid, Valid: true}, AssetID: e.AssetID.UUID}); err != nil {
				return err
			}
		}
		return q.FinishMoveEntry(ctx, db.FinishMoveEntryParams{PlanID: planID, DstRel: e.DstRel, State: "done",
			NewFileID: uuid.NullUUID{UUID: fid, Valid: true}})
	})
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errStop, err)
	}
	return size, nil
}

var errDifferentAtDestination = errors.New("different file at destination")

// copyVerified copies src to dst through a partial file, checks the copy reads back
// identical, keeps the original's modification time and only then gives it its final name.
// If dst already holds the same bytes (an earlier run finished it), it's accepted as is.
func copyVerified(ctx context.Context, src, dst string, mtime time.Time) ([]byte, int64, error) {
	if _, err := os.Stat(dst); err == nil {
		srcSum, _, err := hashFile(ctx, src)
		if err != nil {
			return nil, 0, err
		}
		dstSum, n, err := hashFile(ctx, dst)
		if err != nil {
			return nil, 0, err
		}
		if !bytes.Equal(srcSum, dstSum) {
			return nil, 0, errDifferentAtDestination
		}
		return dstSum, n, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o775); err != nil {
		return nil, 0, err
	}
	partial := dst + partialSuffix
	_ = os.Remove(partial) // left by an interrupted run; always ours
	in, err := os.OpenFile(src, os.O_RDONLY, 0)
	if err != nil {
		return nil, 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return nil, 0, err
	}
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(out, h), ctxReader{ctx, in}, make([]byte, 8<<20))
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(partial)
		return nil, 0, fmt.Errorf("copy: %w", err)
	}
	sum := h.Sum(nil)
	back, m, err := hashFile(ctx, partial)
	if err != nil || m != n || !bytes.Equal(back, sum) {
		_ = os.Remove(partial)
		return nil, 0, errors.New("the copy didn't read back identical to the original")
	}
	if err := os.Chtimes(partial, mtime, mtime); err != nil {
		_ = os.Remove(partial)
		return nil, 0, err
	}
	if _, err := os.Stat(dst); err == nil { // appeared while copying: never overwrite
		_ = os.Remove(partial)
		return nil, 0, errDifferentAtDestination
	}
	if err := os.Rename(partial, dst); err != nil {
		_ = os.Remove(partial)
		return nil, 0, err
	}
	return sum, n, nil
}

func hashFile(ctx context.Context, p string) ([]byte, int64, error) {
	f, err := os.OpenFile(p, os.O_RDONLY, 0)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, ctxReader{ctx, f}, make([]byte, 8<<20))
	return h.Sum(nil), n, err
}

// ctxReader stops a long read (a large video) when the job is cancelled, between buffers.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// within joins a root and a relative path, refusing anything that escapes the root.
func within(root, rel string) (string, error) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if !strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside %s", rel, root)
	}
	return p, nil
}

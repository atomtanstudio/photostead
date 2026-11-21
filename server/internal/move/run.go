package move

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Result is a saved plan and where its review files were written.
type Result struct {
	PlanID  uuid.UUID
	Dir     string // summary.md and plan.csv
	Summary Summary
	Entries []Entry
}

// MakePlan builds a dry run for one person, saves it (for the copy step to carry out exactly as
// reviewed) and writes summary.md and plan.csv under dataDir/move-plans/<plan id>/. It reads the
// index only; nothing in any library is touched.
func MakePlan(ctx context.Context, s *store.Store, owner db.User, dataDir string) (Result, error) {
	rows, err := s.Q.MoveSources(ctx, owner.ID)
	if err != nil {
		return Result{}, err
	}
	sources := make([]Source, len(rows))
	for i, r := range rows {
		sources[i] = Source{AssetID: r.AssetID, Type: r.Type, Day: r.LocalDate.Time, TakenAt: r.TakenAt,
			LiveMotion: r.LiveMotionID, MotionOf: r.MotionOf, StackCover: r.StackCover, FileID: r.FileID,
			LibraryID: r.LibraryID, Library: r.LibraryName, Rel: r.RelPath, Size: r.Size,
			Copies: int(r.Copies), OtherBytes: r.OtherCopiesBytes}
	}
	scRows, err := s.Q.MoveSidecars(ctx, owner.ID)
	if err != nil {
		return Result{}, err
	}
	sidecars := make([]Sidecar, len(scRows))
	for i, r := range scRows {
		sidecars[i] = Sidecar{FileID: r.ID, LibraryID: r.LibraryID, Library: r.LibraryName, Rel: r.RelPath, Size: r.Size}
	}

	copyRows, err := s.Q.MoveCopies(ctx, owner.ID)
	if err != nil {
		return Result{}, err
	}
	copies := make(map[uuid.UUID][]Copy, len(sources))
	for _, r := range copyRows {
		copies[r.AssetID.UUID] = append(copies[r.AssetID.UUID], Copy{LibraryID: r.LibraryID, Rel: r.RelPath})
	}

	entries, unmatched, spare := Build(owner.Username, sources, copies, sidecars)
	sum := Summarize(owner.Username, sources, entries, unmatched)
	sum.SpareSidecars = len(spare)
	raw, err := json.Marshal(sum)
	if err != nil {
		return Result{}, err
	}

	var res Result
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		plan, err := s.Q.WithTx(tx).CreateMovePlan(ctx, db.CreateMovePlanParams{OwnerID: owner.ID, Summary: raw})
		if err != nil {
			return err
		}
		res.PlanID = plan.ID
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"move_plan_entries"},
			[]string{"plan_id", "role", "asset_id", "file_id", "library_id", "src_rel", "dst_rel", "size", "renamed"},
			pgx.CopyFromSlice(len(entries), func(i int) ([]any, error) {
				e := entries[i]
				return []any{plan.ID, e.Role, e.AssetID, e.FileID, e.LibraryID, e.Src, e.Dst, e.Size, e.Renamed}, nil
			}))
		return err
	})
	if err != nil {
		return Result{}, err
	}

	res.Dir = filepath.Join(dataDir, "move-plans", res.PlanID.String())
	if err := os.MkdirAll(res.Dir, 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(res.Dir, "summary.md"), []byte(sum.Markdown(res.PlanID.String())), 0o644); err != nil {
		return Result{}, err
	}
	f, err := os.Create(filepath.Join(res.Dir, "plan.csv"))
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	if err := WriteCSV(f, entries); err != nil {
		return Result{}, err
	}
	res.Summary, res.Entries = sum, entries
	return res, f.Close()
}

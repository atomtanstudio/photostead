// Package refile moves an owner's files in their managed share to the folder of the day the item
// now has, after its date was changed: photos|videos/<year>/<date>/<name>, as the move and uploads
// file them (docs/architecture.md, "Storage layout"). Only files Photostead manages are moved;
// indexed folders (another app's) are never touched, and files kept anywhere but a day folder
// stay where they are.
//
// Every move is written down first (refiles), then made inside a transaction that points the
// file's row at the new path before renaming it on disk, so a scan running alongside never sees it
// as missing or as a new file. A move cut short by a restart is finished, not repeated.
package refile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// dayFolder is a folder files are kept in by date: photos/2026/2026-10-17.
var dayFolder = regexp.MustCompile(`^(photos|videos)/[0-9]{4}/[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// Stats says what a run did.
type Stats struct {
	Moved   int // files now in their day's folder
	Already int // already there
	Kept    int // not in a day folder: left where they are
	Failed  int
}

// Run re-files the owner's files of these items (their Live Photo clips and stacked versions
// included, as the date change was).
func Run(ctx context.Context, s *store.Store, owner uuid.UUID, ids []uuid.UUID, log *slog.Logger) (Stats, error) {
	var st Stats
	rows, err := s.Q.RefileCandidates(ctx, db.RefileCandidatesParams{OwnerID: owner, Ids: ids})
	if err != nil {
		return st, err
	}
	fileIDs := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		fileIDs[i] = r.ID
	}
	// First, any move written down but cut short.
	open, err := s.Q.OpenRefiles(ctx, fileIDs)
	if err != nil {
		return st, err
	}
	finished := map[uuid.UUID]string{} // file -> where it is now
	for _, o := range open {
		if err := finish(ctx, s, o.ID, o.FileID, o.LibraryID, o.RootPath, o.FromRel, o.ToRel, o.Sidecar); err != nil {
			st.Failed++
			if log != nil {
				log.Warn("re-file failed", "file", o.FromRel, "to", o.ToRel, "err", err)
			}
			continue
		}
		st.Moved++
		finished[o.FileID] = o.ToRel
	}
	for _, r := range rows {
		rel := r.RelPath
		if to, ok := finished[r.ID]; ok {
			rel = to
		}
		dir := path.Dir(rel)
		if !dayFolder.MatchString(dir) {
			st.Kept++
			continue
		}
		kind := "videos"
		if r.Type == "image" || r.Clip {
			kind = "photos"
		}
		day := r.Day.Time
		want := fmt.Sprintf("%s/%04d/%s", kind, day.Year(), day.Format("2006-01-02"))
		if dir == want {
			st.Already++
			continue
		}
		to, err := freeName(ctx, s.Q, r.LibraryID, r.RootPath, want, path.Base(rel))
		if err != nil {
			return st, err
		}
		_, scErr := os.Lstat(filepath.Join(r.RootPath, filepath.FromSlash(rel+".xmp")))
		sidecar := scErr == nil
		id, err := s.Q.PlanRefile(ctx, db.PlanRefileParams{FileID: r.ID, FromRel: rel, ToRel: to, Sidecar: sidecar})
		if err != nil {
			return st, err
		}
		if err := finish(ctx, s, id, r.ID, r.LibraryID, r.RootPath, rel, to, sidecar); err != nil {
			st.Failed++
			if log != nil {
				log.Warn("re-file failed", "file", rel, "to", to, "err", err)
			}
			continue
		}
		st.Moved++
		tidy(r.RootPath, dir)
	}
	if log != nil && st.Moved+st.Failed > 0 {
		log.Info("re-filed by date", "moved", st.Moved, "already in place", st.Already, "kept", st.Kept, "failed", st.Failed)
	}
	return st, nil
}

// freeName is a path in the folder for a file named name that nothing else has, on disk or in the
// index: the name itself, or with "-2", "-3"… added, as the move and uploads do.
func freeName(ctx context.Context, q *db.Queries, lib uuid.UUID, root, folder, name string) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		rel := folder + "/" + candidate
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil || !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel+".xmp"))); err == nil {
			continue
		}
		taken, err := q.FileAtPath(ctx, db.FileAtPathParams{LibraryID: lib, RelPath: rel})
		if err != nil {
			return "", err
		}
		if !taken {
			return rel, nil
		}
	}
}

// finish makes one written-down move: the row first, then the file (and its sidecar), in one
// transaction; a file already at its new path (a rename that happened before a restart) only
// needs its row.
func finish(ctx context.Context, s *store.Store, id int64, file, lib uuid.UUID, root, from, to string, sidecar bool) error {
	src := filepath.Join(root, filepath.FromSlash(from))
	dst := filepath.Join(root, filepath.FromSlash(to))
	_, srcErr := os.Lstat(src)
	_, dstErr := os.Lstat(dst)
	moved := errors.Is(srcErr, fs.ErrNotExist) && dstErr == nil
	if !moved && srcErr != nil {
		_ = s.Q.FinishRefile(ctx, db.FinishRefileParams{ID: id, State: "failed", Error: pgtype.Text{String: srcErr.Error(), Valid: true}})
		return srcErr
	}
	err := s.Tx(ctx, func(q *db.Queries) error {
		n, err := q.MoveFileRow(ctx, db.MoveFileRowParams{ID: file, FromRel: from, ToRel: to})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("the index no longer has %s there", from)
		}
		if sidecar {
			sc, err := q.SidecarFile(ctx, db.SidecarFileParams{LibraryID: lib, RelPath: from + ".xmp"})
			if err == nil {
				if _, err := q.MoveFileRow(ctx, db.MoveFileRowParams{ID: sc, FromRel: from + ".xmp", ToRel: to + ".xmp"}); err != nil {
					return err
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if !moved {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
		if sidecar {
			if err := os.Rename(src+".xmp", dst+".xmp"); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return q.FinishRefile(ctx, db.FinishRefileParams{ID: id, State: "done"})
	})
	if err != nil {
		_ = s.Q.FinishRefile(ctx, db.FinishRefileParams{ID: id, State: "failed", Error: pgtype.Text{String: err.Error(), Valid: true}})
	}
	return err
}

// tidy removes a day folder left empty, and its year folder if that's empty too. (os.Remove only
// ever removes an empty folder.)
func tidy(root, dir string) {
	day := filepath.Join(root, filepath.FromSlash(dir))
	if os.Remove(day) == nil {
		_ = os.Remove(filepath.Dir(day))
	}
}

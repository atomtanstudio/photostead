package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/backup"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/watch"
)

type statusOutput struct {
	Body struct {
		Indexing int64         `json:"indexing" doc:"Files waiting to be indexed or rendered"`
		Move     *MoveStatus   `json:"move,omitempty" doc:"The latest move into the managed share, if one has started"`
		Import   *ImportRun    `json:"import,omitempty" doc:"An import from Immich, while it runs"`
		Watching *watch.State  `json:"watching,omitempty" doc:"The folder watcher, when it's running"`
		Search   MeaningStatus `json:"search" doc:"Whether search by meaning is ready"`
		Backup   string        `json:"backup,omitempty" doc:"For the admin: the last backup of the database failed, or none for a day and a half"`
	}
}

type MoveStatus struct {
	Files     int64 `json:"files"`
	Done      int64 `json:"done"`
	Failed    int64 `json:"failed"`
	Bytes     int64 `json:"bytes"`
	BytesDone int64 `json:"bytesDone"`
}

func (s *server) registerStatus() {
	huma.Register(s.api, huma.Operation{
		OperationID: "status", Method: http.MethodGet, Path: "/api/v1/status", Tags: []string{"Status"},
		Summary: "What the server is busy with", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*statusOutput, error) {
		out := &statusOutput{}
		counts, err := s.Store.JobCounts(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range counts {
			if (c.Queue == "images" || c.Queue == "videos") && (c.State == "available" || c.State == "running" || c.State == "retryable") {
				out.Body.Indexing += int64(c.Count)
			}
		}
		m, err := s.Store.Q.LatestMoveProgress(ctx, viewer(ctx))
		switch {
		case err == nil:
			out.Body.Move = &MoveStatus{Files: m.Total, Done: m.Done, Failed: m.Failed, Bytes: m.Bytes, BytesDone: m.BytesDone}
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
		out.Body.Search = s.meaningStatus(ctx)
		if s.Watching != nil {
			w := s.Watching()
			out.Body.Watching = &w
		}
		if s.Backup.Dir != "" && s.admin(ctx) == nil {
			if st, err := backup.Read(ctx, s.Store.Q); err == nil {
				out.Body.Backup = st.Problem(time.Now())
			}
		}
		if s.admin(ctx) == nil && out.Body.Backup == "" {
			if n, err := s.Store.Q.DamagedCount(ctx); err == nil && n > 0 {
				out.Body.Backup = fmt.Sprintf("%d %s no longer %s what %s when %s came in.", n, map[bool]string{true: "file", false: "files"}[n == 1],
					map[bool]string{true: "matches", false: "match"}[n == 1], map[bool]string{true: "it was", false: "they were"}[n == 1],
					map[bool]string{true: "it", false: "they"}[n == 1])
			}
		}
		r, err := s.Store.Q.LatestImport(ctx, db.LatestImportParams{OwnerID: viewer(ctx), Source: "immich"})
		switch {
		case err == nil && r.State == "running":
			out.Body.Import = &ImportRun{State: r.State, Step: r.Step.String, StartedAt: r.StartedAt}
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
		return out, nil
	})
}

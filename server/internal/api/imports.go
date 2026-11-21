package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/immich"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/store/db"
)

type ImmichImport struct {
	Connected bool       `json:"connected" doc:"An Immich server and key are saved"`
	URL       string     `json:"url,omitempty"`
	Run       *ImportRun `json:"run,omitempty" doc:"The latest import"`
}

type ImportRun struct {
	State      string          `json:"state" enum:"running,done,failed"`
	Step       string          `json:"step,omitempty"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Summary    json.RawMessage `json:"summary,omitempty" doc:"What it found and did"`
	Error      string          `json:"error,omitempty"`
}

type immichOutput struct{ Body ImmichImport }

type connectInput struct {
	Body struct {
		URL    string `json:"url" minLength:"1" maxLength:"500" example:"http://192.168.1.10:2283"`
		APIKey string `json:"apiKey" minLength:"1" maxLength:"500" doc:"An Immich API key; kept on this server and never shown again"`
	}
}

type connectOutput struct {
	Body struct {
		Saved bool         `json:"saved" doc:"Saved only when the key can read the items"`
		Check immich.Check `json:"check"`
	}
}

type runImportInput struct {
	Body struct {
		DryRun bool `json:"dryRun,omitempty" doc:"Work out what it would do, and change nothing"`
	}
}

func (s *server) immichState(ctx context.Context) (ImmichImport, error) {
	out := ImmichImport{}
	c, err := s.Store.Q.GetConnection(ctx, db.GetConnectionParams{OwnerID: viewer(ctx), Kind: "immich"})
	switch {
	case err == nil:
		out.Connected, out.URL = true, c.Url
	case !errors.Is(err, pgx.ErrNoRows):
		return out, err
	}
	r, err := s.Store.Q.LatestImport(ctx, db.LatestImportParams{OwnerID: viewer(ctx), Source: "immich"})
	switch {
	case err == nil:
		out.Run = &ImportRun{State: r.State, Step: r.Step.String, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
			Summary: r.Summary, Error: r.Error.String}
	case !errors.Is(err, pgx.ErrNoRows):
		return out, err
	}
	return out, nil
}

func (s *server) registerImports() {
	huma.Register(s.api, huma.Operation{
		OperationID: "getImmichImport", Method: http.MethodGet, Path: "/api/v1/imports/immich", Tags: []string{"Imports"},
		Summary: "The Immich connection and the latest import", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*immichOutput, error) {
		st, err := s.immichState(ctx)
		return &immichOutput{Body: st}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "connectImmich", Method: http.MethodPut, Path: "/api/v1/imports/immich", Tags: []string{"Imports"},
		Summary:     "Check an Immich server and key, and save them if they work",
		Description: "The key needs asset.read; album.read and person.read bring albums and people over too. Nothing is ever written to Immich.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *connectInput) (*connectOutput, error) {
		u, err := url.Parse(strings.TrimSpace(in.Body.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, huma.Error422UnprocessableEntity("Enter the address Immich opens at, like http://192.168.1.10:2283")
		}
		base := strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/")
		key := strings.TrimSpace(in.Body.APIKey)
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		check, err := (&immich.Client{URL: base, Key: key}).Check(cctx)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("Couldn't reach Immich at " + base + ": " + err.Error())
		}
		out := &connectOutput{}
		out.Body.Check = check
		if check.CanImport() {
			if err := s.Store.Q.SaveConnection(ctx, db.SaveConnectionParams{OwnerID: viewer(ctx), Kind: "immich", Url: base, Secret: key}); err != nil {
				return nil, err
			}
			out.Body.Saved = true
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "forgetImmich", Method: http.MethodDelete, Path: "/api/v1/imports/immich", Tags: []string{"Imports"},
		Summary: "Forget the Immich server and key", Description: "What was imported stays.", Security: sessionAuth,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		return nil, s.Store.Q.DeleteConnection(ctx, db.DeleteConnectionParams{OwnerID: viewer(ctx), Kind: "immich"})
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "runImmichImport", Method: http.MethodPost, Path: "/api/v1/imports/immich/run", Tags: []string{"Imports"},
		Summary:     "Import favorites, the archive, albums and people's names from Immich",
		Description: "Runs in the background; follow it with GET /api/v1/imports/immich. Running it again brings over what changed in Immich since.",
		Security:    sessionAuth, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *runImportInput) (*immichOutput, error) {
		st, err := s.immichState(ctx)
		if err != nil {
			return nil, err
		}
		if !st.Connected {
			return nil, huma.Error409Conflict("Connect Immich first.")
		}
		// One at a time; a run marked running for longer than any run takes was cut short.
		if st.Run != nil && st.Run.State == "running" && time.Since(st.Run.StartedAt) < 2*time.Hour {
			return nil, huma.Error409Conflict("An import is already running.")
		}
		if s.Jobs == nil {
			return nil, huma.Error503ServiceUnavailable("Background jobs aren't running.")
		}
		runID, err := immich.Start(ctx, s.Store.Q, viewer(ctx))
		if err != nil {
			return nil, err
		}
		if _, err := s.Jobs.Insert(ctx, jobs.ImmichImportArgs{OwnerID: viewer(ctx), RunID: runID, DryRun: in.Body.DryRun}, nil); err != nil {
			_ = s.Store.Q.FinishImport(ctx, db.FinishImportParams{ID: runID, State: "failed",
				Error: pgtype.Text{String: "Couldn't queue the import: " + err.Error(), Valid: true}})
			return nil, err
		}
		st, err = s.immichState(ctx)
		return &immichOutput{Body: st}, err
	})
}

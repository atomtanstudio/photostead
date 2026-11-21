package api

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store/db"
)

// Editing a photo: turning, flipping, cropping, and its light and color. The original file is
// never changed: the edit is a recipe kept beside it, applied to the previews (and to a download
// of the edited version), and undone by deleting it. Only the owner edits.

const maxEditedPixels = 200_000_000 // 200 MP: a full-size edit takes about 4 bytes a pixel

var fullSize = make(chan struct{}, 2) // full-size edits made at once

type PhotoEdit struct {
	Recipe     *media.Recipe `json:"recipe" doc:"The edit; none: the photo as it is"`
	OrigWidth  int32         `json:"origWidth" doc:"The photo's size before the edit"`
	OrigHeight int32         `json:"origHeight"`
	Width      int32         `json:"width" doc:"Its size with the edit"`
	Height     int32         `json:"height"`
	UpdatedAt  *time.Time    `json:"updatedAt,omitempty"`
}

type photoEditOutput struct {
	Body PhotoEdit
}

type setEditInput struct {
	ID   uuid.UUID `path:"id"`
	Body media.Recipe
}

func (s *server) registerPhotoEdits() {
	huma.Register(s.api, huma.Operation{
		OperationID: "getEdit", Method: http.MethodGet, Path: "/api/v1/assets/{id}/edit", Tags: []string{"Edit"},
		Summary: "A photo's edit", Security: sessionAuth,
	}, func(ctx context.Context, in *assetInput) (*photoEditOutput, error) {
		a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: viewer(ctx), VaultViewer: vaultViewer(ctx), ID: in.ID})
		if err != nil {
			return nil, notFound(err)
		}
		out := &photoEditOutput{Body: PhotoEdit{OrigWidth: a.Width.Int32, OrigHeight: a.Height.Int32, Width: a.Width.Int32, Height: a.Height.Int32}}
		rec, e, err := s.photoEdit(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			out.Body.Recipe, out.Body.OrigWidth, out.Body.OrigHeight, out.Body.UpdatedAt = rec, e.OrigWidth, e.OrigHeight, &e.UpdatedAt
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setEdit", Method: http.MethodPut, Path: "/api/v1/assets/{id}/edit", Tags: []string{"Edit"},
		Summary:     "Edit a photo",
		Description: "Your own photos. The original isn't changed; the previews are made again with the edit (watch the item's thumbHash). An edit that changes nothing undoes it.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *setEditInput) (*photoEditOutput, error) {
		a, err := s.editable(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		rec := in.Body
		if err := rec.Check(); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		if rec.Empty() {
			return s.undoEdit(ctx, a)
		}
		old, e, err := s.photoEdit(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		origW, origH := a.Width.Int32, a.Height.Int32
		if old != nil {
			origW, origH = e.OrigWidth, e.OrigHeight
		}
		if origW <= 0 || origH <= 0 {
			return nil, huma.Error409Conflict("This photo isn't ready yet; try again in a moment.")
		}
		b, _ := json.Marshal(rec)
		w, h := rec.Size(int(origW), int(origH))
		err = s.Store.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetAssetEdit(ctx, db.SetAssetEditParams{AssetID: a.ID, Recipe: b, OrigWidth: origW, OrigHeight: origH, UpdatedBy: uuid.NullUUID{UUID: viewer(ctx), Valid: true}}); err != nil {
				return err
			}
			return q.SetAssetSize(ctx, db.SetAssetSizeParams{ID: a.ID, Owner: a.OwnerID, Width: px(int32(w)), Height: px(int32(h))})
		})
		if err != nil {
			return nil, err
		}
		if err := s.rerender(ctx, a.ID, sameShape(old, &rec)); err != nil {
			return nil, err
		}
		now := time.Now()
		return &photoEditOutput{Body: PhotoEdit{Recipe: &rec, OrigWidth: origW, OrigHeight: origH, Width: int32(w), Height: int32(h), UpdatedAt: &now}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "undoEdit", Method: http.MethodDelete, Path: "/api/v1/assets/{id}/edit", Tags: []string{"Edit"},
		Summary: "Undo a photo's edit", Description: "Back to the original, as it was.", Security: sessionAuth,
	}, func(ctx context.Context, in *assetInput) (*photoEditOutput, error) {
		a, err := s.editable(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return s.undoEdit(ctx, a)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "getUnedited", Method: http.MethodGet, Path: "/api/v1/assets/{id}/edit/base", Tags: []string{"Edit"},
		Summary: "The photo as it is, unedited", Description: "Preview-sized (WebP), for an editor to start from.",
		Security: sessionAuth,
		Responses: map[string]*huma.Response{"200": {Description: "WebP image",
			Content: map[string]*huma.MediaType{"image/webp": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
	}, func(ctx context.Context, in *assetInput) (*huma.StreamResponse, error) {
		a, err := s.editable(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		data, err := s.originalBytes(a)
		if err != nil {
			return nil, err
		}
		out, err := media.Unedited(data)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("This photo couldn't be read.")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			_, w := humago.Unwrap(hctx)
			w.Header().Set("Content-Type", "image/webp")
			w.Header().Set("Cache-Control", "private, no-store")
			_, _ = w.Write(out)
		}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "getEdited", Method: http.MethodGet, Path: "/api/v1/assets/{id}/edited", Tags: []string{"Assets"},
		Summary: "The edited photo, full size", Description: "A JPEG of the whole photo with its edit, without its metadata.",
		Security: sessionAuth,
		Responses: map[string]*huma.Response{"200": {Description: "JPEG",
			Content: map[string]*huma.MediaType{"image/jpeg": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
	}, func(ctx context.Context, in *assetInput) (*huma.StreamResponse, error) {
		a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: viewer(ctx), VaultViewer: vaultViewer(ctx), ID: in.ID})
		if err != nil {
			return nil, notFound(err)
		}
		rec, e, err := s.photoEdit(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, huma.Error404NotFound("This photo hasn't been edited.")
		}
		// The whole picture is decoded: a size limit, and a couple at a time.
		if int64(e.OrigWidth)*int64(e.OrigHeight) > maxEditedPixels {
			return nil, huma.Error422UnprocessableEntity("This photo is too large to edit at full size.")
		}
		select {
		case fullSize <- struct{}{}:
			defer func() { <-fullSize }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		data, err := s.originalBytes(a)
		if err != nil {
			return nil, err
		}
		out, err := media.Edited(data, *rec)
		if err != nil {
			return nil, huma.Error500InternalServerError("The edited photo couldn't be made.")
		}
		name := strings.TrimSuffix(filepath.Base(a.RelPath.String), filepath.Ext(a.RelPath.String)) + " (edited).jpg"
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			_, w := humago.Unwrap(hctx)
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
			w.Header().Set("Cache-Control", "private, no-store")
			_, _ = w.Write(out)
		}}, nil
	})
}

// originalBytes reads a photo's original file.
func (s *server) originalBytes(a db.GetVisibleAssetRow) ([]byte, error) {
	if !a.RootPath.Valid || !a.RelPath.Valid {
		return nil, errNotFound
	}
	data, err := os.ReadFile(filepath.Join(a.RootPath.String, filepath.FromSlash(a.RelPath.String)))
	if err != nil {
		return nil, huma.Error404NotFound("The original file isn't reachable right now.")
	}
	return data, nil
}

// photoEdit is a photo's edit, if it has one.
func (s *server) photoEdit(ctx context.Context, id uuid.UUID) (*media.Recipe, db.AssetEditRow, error) {
	e, err := s.Store.Q.AssetEdit(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, e, nil
	} else if err != nil {
		return nil, e, err
	}
	var rec media.Recipe
	if err := json.Unmarshal(e.Recipe, &rec); err != nil {
		return nil, e, nil
	}
	return &rec, e, nil
}

// editable is one of the viewer's own photos (not a video, which isn't edited here).
func (s *server) editable(ctx context.Context, id uuid.UUID) (db.GetVisibleAssetRow, error) {
	a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: viewer(ctx), VaultViewer: vaultViewer(ctx), ID: id})
	if err != nil {
		return a, notFound(err)
	}
	if a.OwnerID != viewer(ctx) {
		return a, huma.Error403Forbidden("Only its owner can edit this photo.")
	}
	if a.Type != "image" {
		return a, huma.Error422UnprocessableEntity("Only photos can be edited.")
	}
	return a, nil
}

func (s *server) undoEdit(ctx context.Context, a db.GetVisibleAssetRow) (*photoEditOutput, error) {
	old, _, err := s.photoEdit(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	out := &photoEditOutput{Body: PhotoEdit{OrigWidth: a.Width.Int32, OrigHeight: a.Height.Int32, Width: a.Width.Int32, Height: a.Height.Int32}}
	if old == nil {
		return out, nil
	}
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		size, err := q.DeleteAssetEdit(ctx, a.ID)
		if err != nil {
			return err
		}
		out.Body.OrigWidth, out.Body.OrigHeight, out.Body.Width, out.Body.Height = size.OrigWidth, size.OrigHeight, size.OrigWidth, size.OrigHeight
		return q.SetAssetSize(ctx, db.SetAssetSizeParams{ID: a.ID, Owner: a.OwnerID, Width: px(size.OrigWidth), Height: px(size.OrigHeight)})
	})
	if err != nil {
		return nil, err
	}
	return out, s.rerender(ctx, a.ID, sameShape(old, nil))
}

// rerender makes a photo's previews again. keep: what was read from the picture still holds.
func (s *server) rerender(ctx context.Context, id uuid.UUID, keep bool) error {
	if s.Jobs == nil {
		return nil
	}
	_, err := s.Jobs.Insert(ctx, jobs.RenderAssetArgs{AssetID: id, Keep: keep}, jobs.RenderOpts("image"))
	return err
}

// sameShape: two edits turn, flip and crop the same way (only light and color differ), so the
// faces, text and the rest found in the picture are where they were.
func sameShape(a, b *media.Recipe) bool {
	shape := func(r *media.Recipe) string {
		if r == nil {
			return "0"
		}
		c, _ := json.Marshal(struct {
			R int
			F bool
			C *media.Rect
		}{r.Rotate, r.Flip, r.Crop})
		return string(c)
	}
	if a != nil && a.Rotate == 0 && !a.Flip && a.Crop == nil {
		a = nil
	}
	if b != nil && b.Rotate == 0 && !b.Flip && b.Crop == nil {
		b = nil
	}
	return shape(a) == shape(b)
}

func px(v int32) pgtype.Int4 { return pgtype.Int4{Int32: v, Valid: v > 0} }

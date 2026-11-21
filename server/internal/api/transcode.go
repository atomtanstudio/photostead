package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store/db"
)

// Playable copies of videos (see internal/videos): a worker holding a transcode task reads the
// original, makes the copy and sends it back; viewers play it from /assets/{id}/play.

// VideoTask is what a worker needs to make a video's copy.
type VideoTask struct {
	Recipe     string `json:"recipe" doc:"How to make it (media.CopyRecipe)"`
	HDR        bool   `json:"hdr,omitempty" doc:"PQ, HLG or Dolby Vision: tone-map to SDR"`
	Width      int32  `json:"width"`
	Height     int32  `json:"height"`
	DurationMs int32  `json:"durationMs"`
	KeepVideo  bool   `json:"keepVideo,omitempty" doc:"The picture already plays everywhere (H.264, 8-bit, SDR): copy it as it is and change only the sound"`
}

// maxCopy is the largest copy a worker may send (a 1920 px copy of a long film fits easily).
const maxCopy = 16 << 30

// VideoPlayback tells the viewer what to play: the original when the browser can, else the copy.
type VideoPlayback struct {
	Type    string `json:"type,omitempty" doc:"The original's type with its codec, for canPlayType, e.g. video/mp4; codecs=\"hvc1.2.4.L150.B0\""`
	Codec   string `json:"codec" doc:"ffprobe's name for the original's video codec"`
	HDR     bool   `json:"hdr,omitempty"`
	Direct  bool   `json:"direct" doc:"Every browser plays the original"`
	Copy    bool   `json:"copy,omitempty" doc:"There's a copy every browser plays: /assets/{id}/play"`
	Waiting bool   `json:"waiting,omitempty" doc:"Its copy is being made"`
}

// markCopies flags the videos in a list that have a playable copy (hover previews play it: smaller,
// and sure to play).
func (s *server) markCopies(ctx context.Context, list []TimelineAsset) error {
	var ids []uuid.UUID
	for _, a := range list {
		if a.Type == "video" {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.Store.Q.CopiesAmong(ctx, ids)
	if err != nil || len(rows) == 0 {
		return err
	}
	has := make(map[uuid.UUID]bool, len(rows))
	for _, id := range rows {
		has[id] = true
	}
	for i := range list {
		list[i].Copy = has[list[i].ID]
	}
	return nil
}

func (s *server) registerTranscode(mux *http.ServeMux) {
	huma.Register(s.api, huma.Operation{
		OperationID: "workerOriginal", Method: http.MethodGet, Path: "/api/worker/v1/tasks/{id}/original", Tags: []string{"Workers"},
		Summary: "A held transcode task's original video", Description: "Supports Range requests.", Security: workerAuth,
		Responses: map[string]*huma.Response{"200": {Description: "The original file",
			Content: map[string]*huma.MediaType{"application/octet-stream": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
	}, func(ctx context.Context, in *taskInput) (*huma.StreamResponse, error) {
		t, err := s.Store.Q.LeasedTask(ctx, db.LeasedTaskParams{ID: in.ID, WorkerID: uuid.NullUUID{UUID: workerID(ctx), Valid: true}})
		if err != nil || t.Kind != "transcode" {
			return nil, huma.Error404NotFound("This worker doesn't hold that transcode task.")
		}
		a, err := s.Store.Q.GetAssetForRender(ctx, t.AssetID)
		if err != nil {
			return nil, huma.Error404NotFound("No such video.")
		}
		f, err := os.Open(filepath.Join(a.RootPath, filepath.FromSlash(a.RelPath)))
		if err != nil {
			return nil, huma.Error404NotFound("The original file isn't reachable right now.")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer f.Close()
			r, w := humago.Unwrap(hctx)
			info, err := f.Stat()
			if err != nil {
				http.Error(w, "unreadable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, "", info.ModTime(), f)
		}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "workerExtend", Method: http.MethodPost, Path: "/api/worker/v1/tasks/{id}/extend", Tags: []string{"Workers"},
		Summary: "Keep a task ten more minutes", Description: "For long tasks (making a video's copy): call it every few minutes.",
		Security: workerAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *taskInput) (*struct{}, error) {
		n, err := s.Store.Q.ExtendTask(ctx, db.ExtendTaskParams{ID: in.ID, WorkerID: uuid.NullUUID{UUID: workerID(ctx), Valid: true}})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error404NotFound("This worker doesn't hold that task anymore.")
		}
		return nil, nil
	})

	// The copy itself: a streamed body, too big for the JSON API.
	mux.HandleFunc("PUT /api/worker/v1/tasks/{id}/copy", s.receiveCopy)

	huma.Register(s.api, huma.Operation{
		OperationID: "playVideo", Method: http.MethodGet, Path: "/api/v1/assets/{id}/play", Tags: []string{"Assets"},
		Summary: "A video's copy that every browser plays", Description: "H.264 in MP4, SDR, at most 1920 px. Supports Range requests. " +
			"Only for videos whose detail says video.copy.",
		Security: sessionAuth,
		Responses: map[string]*huma.Response{"200": {Description: "MP4 video",
			Content: map[string]*huma.MediaType{"video/mp4": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
	}, func(ctx context.Context, in *originalInput) (*huma.StreamResponse, error) {
		a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: viewer(ctx), VaultViewer: vaultViewer(ctx), ID: in.ID})
		if err != nil {
			return nil, notFound(err)
		}
		f, err := os.Open(s.Render.PlayPath(in.ID))
		if err != nil {
			return nil, huma.Error404NotFound("This video has no copy (yet).")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer f.Close()
			r, w := humago.Unwrap(hctx)
			info, err := f.Stat()
			if err != nil {
				http.Error(w, "unreadable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Cache-Control", cacheFor(a.Visibility, "private, max-age=3600"))
			http.ServeContent(w, r, "", info.ModTime(), f)
		}}, nil
	})
}

// receiveCopy takes a worker's copy of a video it holds the transcode task for: kept only once it's
// whole and plays as H.264.
func (s *server) receiveCopy(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		replyError(w, http.StatusUnauthorized, "A worker token is needed.")
		return
	}
	ctx := r.Context()
	wk, err := s.Store.Q.WorkerByToken(ctx, WorkerTokenHash(token))
	if err != nil {
		replyError(w, http.StatusUnauthorized, "That worker token isn't valid (or was revoked).")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		replyError(w, http.StatusNotFound, "No such task.")
		return
	}
	owner := uuid.NullUUID{UUID: wk.ID, Valid: true}
	t, err := s.Store.Q.LeasedTask(ctx, db.LeasedTaskParams{ID: id, WorkerID: owner})
	if err != nil || t.Kind != "transcode" {
		replyError(w, http.StatusNotFound, "This worker doesn't hold that transcode task.")
		return
	}
	encoder := r.URL.Query().Get("encoder")
	if encoder == "" || len(encoder) > 40 {
		replyError(w, http.StatusBadRequest, "Say which encoder made it (?encoder=).")
		return
	}
	dst := s.Render.PlayPath(t.AssetID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		replyError(w, http.StatusInternalServerError, "Couldn't keep the copy.")
		return
	}
	part := dst + ".part"
	f, err := os.Create(part)
	if err != nil {
		replyError(w, http.StatusInternalServerError, "Couldn't keep the copy.")
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, maxCopy))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(part)
		replyError(w, http.StatusBadRequest, "The copy didn't arrive whole.")
		return
	}
	v, err := media.Probe(ctx, part)
	if err != nil || v.Codec != "h264" || !v.Direct() {
		_ = os.Remove(part)
		replyError(w, http.StatusUnprocessableEntity, "That isn't a copy every browser plays (H.264, 8-bit, in MP4).")
		return
	}
	if err := os.Rename(part, dst); err != nil {
		_ = os.Remove(part)
		replyError(w, http.StatusInternalServerError, "Couldn't keep the copy.")
		return
	}
	if _, err := s.Store.Q.FinishCopy(ctx, db.FinishCopyParams{TaskID: id, WorkerID: owner, Recipe: media.CopyRecipe, Encoder: encoder,
		Width: int32(v.Width), Height: int32(v.Height), Bytes: n}); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Warn("a video's copy arrived, but its task is gone", "task", id, "err", err)
		}
		_ = os.Remove(dst)
		replyError(w, http.StatusConflict, "This worker doesn't hold that task anymore.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"saved": true, "bytes": n})
}

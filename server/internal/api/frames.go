package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store/db"
)

// Keeping a moment of a video as a photo: the frame at a time, full size, filed in the viewer's
// library like an upload (dated when the moment was), and indexed. The video isn't changed.

type frameRequest struct {
	AtMs int64 `json:"atMs"` // how far into the video
}

func (s *server) saveFrame(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := s.member(r)
	if !ok {
		replyError(w, http.StatusUnauthorized, "Sign in first.")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		replyError(w, http.StatusNotFound, "No such item.")
		return
	}
	var in frameRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || in.AtMs < 0 {
		replyError(w, http.StatusBadRequest, `Send {"atMs": how far into the video}.`)
		return
	}
	ctx := r.Context()
	a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: owner, VaultViewer: uuid.Nil, ID: id})
	if err != nil || a.TrashedAt != nil {
		replyError(w, http.StatusNotFound, "No such item.")
		return
	}
	if a.Type != "video" || !a.RootPath.Valid || !a.RelPath.Valid {
		replyError(w, http.StatusUnprocessableEntity, "Only a video has frames to keep.")
		return
	}
	if a.DurationMs.Valid && in.AtMs > int64(a.DurationMs.Int32) {
		in.AtMs = int64(a.DurationMs.Int32)
	}
	lib, ok := s.managedLibrary(ctx, w, owner)
	if !ok {
		return
	}
	src := filepath.Join(a.RootPath.String, filepath.FromSlash(a.RelPath.String))
	frame, err := media.Frame(ctx, src, float64(in.AtMs)/1000)
	if err != nil && in.AtMs > 0 {
		// Past the last frame (the length is rounded): a moment earlier.
		frame, err = media.Frame(ctx, src, max(0, float64(in.AtMs)/1000-0.5))
	}
	if err != nil {
		slog.Warn("frame grab failed", "asset", id, "err", err)
		replyError(w, http.StatusUnprocessableEntity, "That frame couldn't be read.")
		return
	}
	staging := filepath.Join(lib.RootPath, ".uploads")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.CreateTemp(staging, "frame-*.part")
	if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp) // filed, it's moved away first
	_, err = f.Write(frame)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sum := sha256.Sum256(frame)
	at := time.Duration(in.AtMs) * time.Millisecond
	base := strings.TrimSuffix(filepath.Base(a.RelPath.String), filepath.Ext(a.RelPath.String))
	meta := UploadMeta{
		FileName: fmt.Sprintf("%s frame %dm%02d.%01ds.jpg", base, int(at.Minutes()), int(at.Seconds())%60, int(at.Milliseconds()%1000)/100),
		TakenAt:  a.TakenAt.Add(at),
	}
	s.file(ctx, w, owner, "frame", lib, tmp, sum[:], int64(len(frame)), meta, nil)
}

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

// Trimming a video: the part between two times is copied (not re-encoded) into a new video, filed
// in the viewer's library like an upload and dated when that part starts. The video itself isn't
// changed.

type trimRequest struct {
	StartMs int64 `json:"startMs"`
	EndMs   int64 `json:"endMs"`
}

func (s *server) saveTrim(w http.ResponseWriter, r *http.Request) {
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
	var in trimRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || in.StartMs < 0 || in.EndMs-in.StartMs < 500 {
		replyError(w, http.StatusBadRequest, `Send {"startMs", "endMs"}: a part at least half a second long.`)
		return
	}
	ctx := r.Context()
	a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: owner, VaultViewer: uuid.Nil, ID: id})
	if err != nil || a.TrashedAt != nil {
		replyError(w, http.StatusNotFound, "No such item.")
		return
	}
	if a.Type != "video" || !a.RootPath.Valid || !a.RelPath.Valid {
		replyError(w, http.StatusUnprocessableEntity, "Only a video can be trimmed.")
		return
	}
	if d := int64(a.DurationMs.Int32); a.DurationMs.Valid && in.EndMs > d {
		in.EndMs = d
	}
	if in.EndMs-in.StartMs < 500 {
		replyError(w, http.StatusBadRequest, "That part is too short.")
		return
	}
	lib, ok := s.managedLibrary(ctx, w, owner)
	if !ok {
		return
	}
	staging := filepath.Join(lib.RootPath, ".uploads")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ext := strings.ToLower(filepath.Ext(a.RelPath.String))
	if ext != ".mp4" && ext != ".mov" && ext != ".m4v" {
		ext = ".mp4" // other containers are copied into MP4
	}
	f, err := os.CreateTemp(staging, "trim-*"+ext)
	if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp) // filed, it's moved away first
	src := filepath.Join(a.RootPath.String, filepath.FromSlash(a.RelPath.String))
	if err := media.Trim(ctx, src, tmp, float64(in.StartMs)/1000, float64(in.EndMs)/1000); err != nil {
		slog.Warn("trim failed", "asset", id, "err", err)
		replyError(w, http.StatusUnprocessableEntity, "That video couldn't be trimmed.")
		return
	}
	sum, size, err := hashFile(tmp)
	if err != nil || size == 0 {
		replyError(w, http.StatusUnprocessableEntity, "That video couldn't be trimmed.")
		return
	}
	start := time.Duration(in.StartMs) * time.Millisecond
	base := strings.TrimSuffix(filepath.Base(a.RelPath.String), filepath.Ext(a.RelPath.String))
	meta := UploadMeta{
		FileName: fmt.Sprintf("%s trimmed %dm%02ds-%ds%s", base, int(start.Minutes()), int(start.Seconds())%60, (in.EndMs-in.StartMs+500)/1000, ext),
		TakenAt:  a.TakenAt.Add(start),
	}
	s.file(ctx, w, owner, "trim", lib, tmp, sum, size, meta, nil)
}

func hashFile(path string) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return h.Sum(nil), n, err
}

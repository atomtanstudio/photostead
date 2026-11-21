package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/store/db"
)

// Keeping a moment of a video as a photo: the frame, full size, is filed in the photostead share,
// dated when the moment was, and becomes a photo. A photo has no frames to keep.
func TestVideoFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, owner, url, c := signedIn(t, ctx)
	var video, photo uuid.UUID
	var taken time.Time
	var w, h int32
	if err := s.Pool.QueryRow(ctx, `SELECT id, taken_at, width, height FROM assets WHERE type = 'video' LIMIT 1`).Scan(&video, &taken, &w, &h); err != nil {
		t.Fatal(err)
	}
	_ = s.Pool.QueryRow(ctx, `SELECT id FROM assets WHERE type = 'image' LIMIT 1`).Scan(&photo)

	frame := func(id uuid.UUID, body string) (int, map[string]any) {
		resp := do(t, c, http.MethodPost, url+"/api/v1/assets/"+id.String()+"/frame", body, map[string]string{"Content-Type": "application/json"})
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, _ := frame(video, `{"atMs":500}`); code != http.StatusConflict {
		t.Errorf("with nowhere to file it: %d", code)
	}
	share := t.TempDir()
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photostead", Kind: "managed", RootPath: share, ReadOnly: false, SidecarMode: "off"}); err != nil {
		t.Fatal(err)
	}
	if code, _ := frame(photo, `{"atMs":0}`); code != http.StatusUnprocessableEntity {
		t.Errorf("a photo's frame: %d", code)
	}
	code, out := frame(video, `{"atMs":500}`)
	rel, _ := out["path"].(string)
	if code != http.StatusAccepted || !strings.HasSuffix(rel, " frame 0m00.5s.jpg") {
		t.Fatalf("frame: %d %v", code, out)
	}
	b, err := os.ReadFile(filepath.Join(share, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	img, err := vips.NewImageFromBuffer(b)
	if err != nil {
		t.Fatal(err)
	}
	if fw, fh := img.Width(), img.Height(); (fw != int(w) || fh != int(h)) && (fw != int(h) || fh != int(w)) {
		t.Errorf("the frame is %dx%d, the video %dx%d", fw, fh, w, h)
	}
	img.Close()

	insert, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var file uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM files WHERE rel_path = $1`, rel).Scan(&file); err != nil {
		t.Fatal(err)
	}
	if _, err := insert.Insert(ctx, jobs.IngestFileArgs{FileID: file}, jobs.RenderOpts("image")); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)
	var kind string
	var at time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT a.type, a.taken_at FROM assets a JOIN files f ON f.asset_id = a.id WHERE f.id = $1`, file).Scan(&kind, &at); err != nil {
		t.Fatal(err)
	}
	if want := taken.Add(500 * time.Millisecond); kind != "image" || at.Sub(want).Abs() > 2*time.Second {
		t.Errorf("the new photo: %s taken %v, want %v", kind, at, want)
	}
	// Asking again is the same frame: already there.
	if code, out := frame(video, `{"atMs":500}`); code != http.StatusOK || out["status"] != "duplicate" {
		t.Errorf("again: %d %v", code, out)
	}

	// Trimmed: the part is copied into a new video beside it; the video isn't changed.
	trim := func(id uuid.UUID, body string) (int, map[string]any) {
		resp := do(t, c, http.MethodPost, url+"/api/v1/assets/"+id.String()+"/trim", body, map[string]string{"Content-Type": "application/json"})
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	var duration int32
	var src string
	_ = s.Pool.QueryRow(ctx, `SELECT a.duration_ms, l.root_path || '/' || f.rel_path FROM assets a JOIN files f ON f.id = a.primary_file_id
		JOIN libraries l ON l.id = f.library_id WHERE a.id = $1`, video).Scan(&duration, &src)
	before := snapshot(t, src)
	if code, _ := trim(video, `{"startMs":0,"endMs":100}`); code != http.StatusBadRequest {
		t.Errorf("too short a part: %d", code)
	}
	if code, _ := trim(photo, `{"startMs":0,"endMs":1000}`); code != http.StatusUnprocessableEntity {
		t.Errorf("trimming a photo: %d", code)
	}
	code, out = trim(video, fmt.Sprintf(`{"startMs":0,"endMs":%d}`, duration/2))
	rel, _ = out["path"].(string)
	if code != http.StatusAccepted || !strings.Contains(rel, " trimmed 0m00s-") {
		t.Fatalf("trim: %d %v", code, out)
	}
	if info, err := os.Stat(filepath.Join(share, filepath.FromSlash(rel))); err != nil || info.Size() == 0 {
		t.Errorf("the trimmed copy: %v", err)
	}
	if snapshot(t, src) != before {
		t.Error("the video changed")
	}
}

package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/jobs"
)

// Editing a photo: turned and cropped, its size is the edited size at once, its previews are made
// again with the edit, and the edited download is the whole photo edited; undone, it's back to the
// original. The original file isn't touched.
func TestPhotoEdit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, url, c := signedIn(t, ctx)
	var id uuid.UUID
	var w, h int32
	var hash []byte
	var path string
	err := s.Pool.QueryRow(ctx, `SELECT a.id, a.width, a.height, a.thumbhash, l.root_path || '/' || f.rel_path
		FROM assets a JOIN files f ON f.id = a.primary_file_id JOIN libraries l ON l.id = f.library_id
		WHERE a.type = 'image' AND a.width <> a.height AND a.render_state = 'done' ORDER BY f.rel_path LIMIT 1`).Scan(&id, &w, &h, &hash, &path)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, path)
	base := url + "/api/v1/assets/" + id.String()

	if code := doJSON(t, c, http.MethodPut, base+"/edit", `{"rotate":45}`, nil); code != 422 {
		t.Errorf("rotate 45: %d", code)
	}
	if code := doJSON(t, c, http.MethodPut, base+"/edit", `{"rotate":0,"crop":{"x":0.8,"y":0,"w":0.5,"h":1}}`, nil); code != 422 {
		t.Errorf("crop outside: %d", code)
	}

	var e struct {
		Width, Height, OrigWidth, OrigHeight int32
		Recipe                               *struct{ Rotate int }
	}
	if code := doJSON(t, c, http.MethodPut, base+"/edit", `{"rotate":90,"crop":{"x":0,"y":0,"w":0.5,"h":1},"exposure":0.5,"saturation":-1}`, &e); code != 200 {
		t.Fatalf("edit: %d", code)
	}
	// Turned a quarter, the width is the old height; half of that is kept.
	wantW, wantH := (h+1)/2, w
	if e.OrigWidth != w || e.OrigHeight != h || e.Width < wantW-1 || e.Width > wantW || e.Height != wantH || e.Recipe == nil || e.Recipe.Rotate != 90 {
		t.Errorf("edit said %+v (was %dx%d)", e, w, h)
	}
	var a struct {
		Width, Height int32
		Edited        bool
	}
	getJSON(t, c, base, &a)
	if !a.Edited || a.Width != e.Width || a.Height != e.Height {
		t.Errorf("the item after the edit: %+v", a)
	}

	insert, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insert.Insert(ctx, jobs.RenderAssetArgs{AssetID: id}, jobs.RenderOpts("image")); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)
	var after []byte
	var rw, rh int32
	_ = s.Pool.QueryRow(ctx, `SELECT thumbhash, width, height FROM assets WHERE id = $1`, id).Scan(&after, &rw, &rh)
	if bytes.Equal(after, hash) || rw != e.Width || rh != e.Height {
		t.Errorf("after the render: %dx%d, thumbhash changed %v", rw, rh, !bytes.Equal(after, hash))
	}
	pw, ph := imageSize(t, c, base+"/preview?v=x") // the web app adds a version, so edits aren't cached
	if (pw > ph) != (e.Width > e.Height) {
		t.Errorf("the preview is %dx%d, the edit %dx%d", pw, ph, e.Width, e.Height)
	}
	if bw, bh := imageSize(t, c, base+"/edit/base"); (bw > bh) != (w > h) {
		t.Errorf("the unedited base is %dx%d, the photo %dx%d", bw, bh, w, h)
	}
	if ew, eh := imageSize(t, c, base+"/edited"); ew != int(e.Width) || eh != int(e.Height) {
		t.Errorf("the edited download is %dx%d, not %dx%d", ew, eh, e.Width, e.Height)
	}

	// Undone: the size it was, and no edit.
	if code := doJSON(t, c, http.MethodDelete, base+"/edit", "", &e); code != 200 || e.Width != w || e.Height != h || e.Recipe != nil {
		t.Errorf("undo: %d %+v", code, e)
	}
	a.Edited = false
	getJSON(t, c, base, &a)
	if a.Edited || a.Width != w || a.Height != h {
		t.Errorf("the item after undoing: %+v", a)
	}
	if code := status(t, c, http.MethodGet, base+"/edited", ""); code != 404 {
		t.Errorf("edited download with no edit: %d", code)
	}
	if snapshot(t, path) != before {
		t.Error("the original file changed")
	}
	// The original, to save: an attachment with its own name.
	resp := do(t, c, http.MethodGet, base+"/original?download=1", "", nil)
	resp.Body.Close()
	if cd := resp.Header.Get("Content-Disposition"); resp.StatusCode != 200 || !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, filepath.Base(path)) {
		t.Errorf("downloading the original: %d %q", resp.StatusCode, cd)
	}
}

func imageSize(t *testing.T, c *http.Client, u string) (int, int) {
	t.Helper()
	resp := do(t, c, http.MethodGet, u, "", nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d %s", u, resp.StatusCode, b)
	}
	img, err := vips.NewImageFromBuffer(b)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	return img.Width(), img.Height()
}

package e2e

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"go.n16f.net/thumbhash"

	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/search"
)

// A video re-rendered with a new poster (its old one was a black frame) is read again: search,
// text, faces, pets, labels and look-alikes. Re-rendered to the same picture, nothing is redone.
func TestRerenderedPosterIsReadAgain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, _, _ := signedIn(t, ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var video uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM assets WHERE type = 'video' AND motion_of IS NULL AND render_state = 'done' LIMIT 1`).Scan(&video); err != nil {
		t.Fatal(err)
	}
	analysed := func() {
		t.Helper()
		v := make([]string, 768)
		for i := range v {
			v[i] = "0"
		}
		v[0] = "1"
		exec(`INSERT INTO asset_embeddings (asset_id, model, embedding) VALUES ($1, $2, $3::halfvec) ON CONFLICT DO NOTHING`, video, search.Model, "["+strings.Join(v, ",")+"]")
		exec(`INSERT INTO asset_ocr (asset_id, engine, text) VALUES ($1, 'test', '') ON CONFLICT DO NOTHING`, video)
		exec(`INSERT INTO face_scans (asset_id, engine, faces) VALUES ($1, 'test', 0) ON CONFLICT DO NOTHING`, video)
		exec(`INSERT INTO pet_scans (asset_id, engine, pets) VALUES ($1, 'test', 0) ON CONFLICT DO NOTHING`, video)
		exec(`INSERT INTO labels_checked (asset_id, version, had_text) VALUES ($1, 1, false) ON CONFLICT DO NOTHING`, video)
		exec(`INSERT INTO near_duplicate_checks (asset_id) VALUES ($1) ON CONFLICT DO NOTHING`, video)
	}
	left := func() int {
		t.Helper()
		var n int
		if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM asset_embeddings WHERE asset_id = $1) + (SELECT count(*) FROM asset_ocr WHERE asset_id = $1)
			+ (SELECT count(*) FROM face_scans WHERE asset_id = $1) + (SELECT count(*) FROM pet_scans WHERE asset_id = $1)
			+ (SELECT count(*) FROM labels_checked WHERE asset_id = $1) + (SELECT count(*) FROM near_duplicate_checks WHERE asset_id = $1)`, video).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	rc, err := river.NewClient[pgx.Tx](riverpgxv5.New(s.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	rerender := func() []byte {
		t.Helper()
		if _, err := rc.Insert(ctx, jobs.RenderAssetArgs{AssetID: video}, jobs.RenderOpts("video")); err != nil {
			t.Fatal(err)
		}
		waitForJobs(t, ctx, s)
		var h []byte
		_ = s.Pool.QueryRow(ctx, `SELECT thumbhash FROM assets WHERE id = $1`, video).Scan(&h)
		return h
	}

	// The poster as it was before posters skipped black frames.
	black := thumbhash.EncodeImage(image.NewRGBA(image.Rect(0, 0, 32, 18)))
	exec(`UPDATE assets SET thumbhash = $2 WHERE id = $1`, video, black)
	analysed()
	if h := rerender(); bytes.Equal(h, black) || len(h) == 0 {
		t.Fatalf("the poster wasn't redone: %x", h)
	}
	if n := left(); n != 0 {
		t.Errorf("%d analyses of the old poster are left", n)
	}

	// Same picture again: what was read from it stays.
	analysed()
	rerender()
	if n := left(); n != 6 {
		t.Errorf("a re-render to the same picture dropped analyses: %d of 6 left", n)
	}
}

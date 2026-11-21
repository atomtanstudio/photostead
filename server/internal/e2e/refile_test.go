package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/refile"
	"photostead.com/server/internal/store/db"
)

// A date change re-files the item's file in the managed share: it moves to the new day's folder
// with its sidecar, the old folders go if they're left empty, a name taken there gets a number,
// a file kept outside the day folders stays put, and a rescan afterwards changes nothing.
func TestRefileOnDateChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)

	// A stand-in managed share: one photo in a day folder (with a sidecar), one kept elsewhere.
	root := t.TempDir()
	put := func(src, rel string) {
		t.Helper()
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("../testdata/pairs/a/beach.jpg", "photos/2020/2020-01-01/beach.jpg")
	put("../testdata/pairs/a/beach.jpg.xmp", "photos/2020/2020-01-01/beach.jpg.xmp")
	put("../testdata/pairs/b/Beach.JPG", "Inbox/other.jpg")
	lib, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photostead", Kind: "managed", RootPath: root, ReadOnly: false, SidecarMode: "shadow"})
	if err != nil {
		t.Fatal(err)
	}
	if err := media.Start(2); err != nil {
		t.Fatal(err)
	}
	exif, err := metadata.NewReader(2)
	if err != nil {
		t.Fatal(err)
	}
	defer exif.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	render := media.Renderer{DataDir: t.TempDir()}
	rc, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{
		Logger: log, FetchPollInterval: 100 * time.Millisecond,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}, jobs.QueueImages: {MaxWorkers: 2}, jobs.QueueVideos: {MaxWorkers: 1}},
		Workers: jobs.Workers(&jobs.Deps{Store: s, Exif: exif, Render: render, Zone: time.UTC, Log: log, TmpDir: t.TempDir()}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer rc.Stop(context.Background())
	scan := func() {
		t.Helper()
		if _, err := rc.Insert(ctx, jobs.ScanLibraryArgs{LibraryID: lib.ID}, nil); err != nil {
			t.Fatal(err)
		}
		waitForJobs(t, ctx, s)
	}
	scan()
	idOf := func(rel string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.Pool.QueryRow(ctx, `SELECT asset_id FROM files WHERE library_id = $1 AND rel_path = $2`, lib.ID, rel).Scan(&id); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		return id
	}
	beach, other := idOf("photos/2020/2020-01-01/beach.jpg"), idOf("Inbox/other.jpg")

	h, _ := api.Handler(api.Options{Store: s, Render: render, Jobs: rc})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	redate := func(id uuid.UUID, when string) {
		t.Helper()
		if code := doJSON(t, c, "POST", srv.URL+"/api/v1/assets/date", mustJSON(map[string]any{"assetIds": []uuid.UUID{id}, "setEarliest": when}), nil); code != 200 {
			t.Fatalf("redate = %d", code)
		}
		waitForJobs(t, ctx, s)
	}
	exists := func(rel string) bool {
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}

	redate(beach, "2022-03-04T10:00")
	if !exists("photos/2022/2022-03-04/beach.jpg") || !exists("photos/2022/2022-03-04/beach.jpg.xmp") {
		t.Error("the photo (or its sidecar) isn't in the new day's folder")
	}
	if exists("photos/2020/2020-01-01") || exists("photos/2020") {
		t.Error("the emptied day (and year) folders are still there")
	}
	var rel, state string
	_ = s.Pool.QueryRow(ctx, `SELECT f.rel_path, r.state FROM files f JOIN refiles r ON r.file_id = f.id WHERE f.asset_id = $1`, beach).Scan(&rel, &state)
	if rel != "photos/2022/2022-03-04/beach.jpg" || state != "done" {
		t.Errorf("index says %s, the move is %s", rel, state)
	}
	if code := status(t, c, "GET", srv.URL+"/api/v1/assets/"+beach.String()+"/original", ""); code != 200 {
		t.Errorf("the original from its new place = %d", code)
	}

	// A rescan finds every file where the index has it: nothing new, nothing missing.
	var before int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM files WHERE library_id = $1`, lib.ID).Scan(&before)
	scan()
	var after, missing, assets int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE state = 'missing') FROM files WHERE library_id = $1`, lib.ID).Scan(&after, &missing)
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets`).Scan(&assets)
	if after != before || missing != 0 || assets != 2 {
		t.Errorf("after a rescan: %d files (was %d), %d missing, %d items", after, before, missing, assets)
	}

	// A name already taken in the new folder: the moved file gets a number.
	put("../testdata/pairs/b/Beach.JPG", "photos/2023/2023-01-01/beach.jpg")
	redate(beach, "2023-01-01T09:00")
	if !exists("photos/2023/2023-01-01/beach-2.jpg") || !exists("photos/2023/2023-01-01/beach-2.jpg.xmp") || !exists("photos/2023/2023-01-01/beach.jpg") {
		t.Error("the clash wasn't settled with -2")
	}
	if exists("photos/2022") {
		t.Error("the 2022 folder was left behind")
	}

	// A move cut short by a restart, after the rename but before the index caught up: the next run
	// finishes it rather than moving the file again.
	var fileID uuid.UUID
	_ = s.Pool.QueryRow(ctx, `SELECT id FROM files WHERE asset_id = $1 AND library_id = $2`, beach, lib.ID).Scan(&fileID)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO refiles (file_id, from_rel, to_rel) VALUES ($1, 'photos/2023/2023-01-01/beach-2.jpg', 'photos/2023/2023-01-01/beach-3.jpg')`, fileID); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "photos/2023/2023-01-01/beach-2.jpg"), filepath.Join(root, "photos/2023/2023-01-01/beach-3.jpg")); err != nil {
		t.Fatal(err)
	}
	if st, err := refile.Run(ctx, s, owner.ID, []uuid.UUID{beach}, nil); err != nil || st.Moved != 1 || st.Failed != 0 {
		t.Errorf("finishing a cut-short move = %+v %v", st, err)
	}
	_ = s.Pool.QueryRow(ctx, `SELECT rel_path FROM files WHERE id = $1`, fileID).Scan(&rel)
	if rel != "photos/2023/2023-01-01/beach-3.jpg" || !exists("photos/2023/2023-01-01/beach-3.jpg") {
		t.Errorf("after finishing, the index says %s", rel)
	}

	// A file kept outside the day folders stays where it is.
	redate(other, "2019-05-05T12:00")
	if !exists("Inbox/other.jpg") {
		t.Error("a file outside the day folders was moved")
	}
}

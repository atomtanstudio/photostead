package e2e

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/watch"
)

func TestFolderWatcher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	libDir := filepath.Join(t.TempDir(), "photos")
	copyTree(t, "../testdata/pairs", libDir)
	if err := os.MkdirAll(filepath.Join(libDir, "private"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
	lib, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photos", Kind: "indexed", RootPath: libDir,
		ReadOnly: true, SidecarMode: "shadow", Watch: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Q.SetLibraryExclude(ctx, db.SetLibraryExcludeParams{ID: lib.ID, Exclude: []string{"private"}}); err != nil {
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
	rc, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{
		Logger: log, FetchPollInterval: 100 * time.Millisecond,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}, jobs.QueueImages: {MaxWorkers: 2}, jobs.QueueVideos: {MaxWorkers: 1}},
		Workers: jobs.Workers(&jobs.Deps{Store: s, Exif: exif, Render: media.Renderer{DataDir: t.TempDir()}, Zone: time.UTC, Log: log, TmpDir: t.TempDir()}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer rc.Stop(context.Background())
	if _, err := rc.Insert(ctx, jobs.ScanLibraryArgs{LibraryID: lib.ID}, nil); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)

	startWatcher := func() (*watch.Watcher, context.CancelFunc) {
		wctx, stop := context.WithCancel(ctx)
		w := &watch.Watcher{Store: s, Jobs: rc, Log: log, Quiet: 300 * time.Millisecond, MaxWait: 2 * time.Second, Reload: 500 * time.Millisecond}
		go w.Run(wctx)
		deadline := time.Now().Add(20 * time.Second)
		for w.State().Folders == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the watcher didn't start watching")
			}
			time.Sleep(50 * time.Millisecond)
		}
		return w, stop
	}
	state := func(rel string) string {
		var st string
		var hasAsset bool
		err := s.Pool.QueryRow(ctx, `SELECT state, asset_id IS NOT NULL FROM files WHERE library_id = $1 AND rel_path = $2`, lib.ID, rel).Scan(&st, &hasAsset)
		if err != nil {
			return "none"
		}
		if st == "present" && !hasAsset {
			return "present without an item"
		}
		return st
	}
	waitState := func(rel, want string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for state(rel) != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s is %q, want %q", rel, state(rel), want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	photo, err := os.ReadFile(filepath.Join(libDir, "a/beach.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	put := func(rel string) {
		t.Helper()
		b := slices.Clone(photo)
		p := filepath.Join(libDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(b, []byte(rel)...), 0o644); err != nil { // different bytes, a new item
			t.Fatal(err)
		}
	}

	w, stop := startWatcher()
	if st := w.State(); st.Libraries != 1 || st.Problem != "" {
		t.Errorf("state = %+v", st)
	}

	put("a/new.jpg") // a new photo in a folder that's already there
	waitState("a/new.jpg", "present")

	put("trip/2026/day1.jpg") // a new folder, two deep
	waitState("trip/2026/day1.jpg", "present")
	put("trip/2026/day2.jpg") // and a photo added to it afterwards: the new folder is watched too
	waitState("trip/2026/day2.jpg", "present")

	if err := os.Remove(filepath.Join(libDir, "a/beach.jpg")); err != nil {
		t.Fatal(err)
	}
	waitState("a/beach.jpg", "missing")

	if err := os.Rename(filepath.Join(libDir, "trip"), filepath.Join(libDir, "trip-renamed")); err != nil {
		t.Fatal(err)
	}
	waitState("trip-renamed/2026/day1.jpg", "present")
	waitState("trip/2026/day1.jpg", "missing")
	put("trip-renamed/2026/day3.jpg") // the moved folder is still watched, under its new name
	waitState("trip-renamed/2026/day3.jpg", "present")

	put("private/secret.jpg")                                                           // an excluded folder
	_ = os.WriteFile(filepath.Join(libDir, "a", ".DS_Store"), []byte("clutter"), 0o644) // OS clutter
	put("a/marker.jpg")                                                                 // after both, so they've had their chance
	waitState("a/marker.jpg", "present")
	waitForJobs(t, ctx, s)
	if got := state("private/secret.jpg"); got != "none" {
		t.Errorf("a file in an excluded folder was recorded: %s", got)
	}
	if got := state("a/.DS_Store"); got != "none" {
		t.Errorf("OS clutter was recorded: %s", got)
	}

	// While nothing watches, a photo arrives; the next start catches up on it from the folder's time.
	stop()
	time.Sleep(300 * time.Millisecond)
	put("b/while-stopped.jpg")
	_, stop = startWatcher()
	defer stop()
	waitState("b/while-stopped.jpg", "present")
}

func TestDailySchedule(t *testing.T) {
	la, _ := time.LoadLocation("America/Los_Angeles")
	d := jobs.Daily{Hour: 4, Minute: 30, Zone: la}
	cases := []struct{ now, want string }{
		{"2026-10-17T03:00:00-07:00", "2026-10-17T04:30:00-07:00"}, // later today
		{"2026-10-17T04:30:00-07:00", "2026-10-18T04:30:00-07:00"}, // just ran: tomorrow
		{"2026-10-17T23:00:00-07:00", "2026-10-18T04:30:00-07:00"},
		{"2026-11-01T01:00:00-07:00", "2026-11-01T04:30:00-08:00"}, // the night clocks go back
	}
	for _, c := range cases {
		now, _ := time.Parse(time.RFC3339, c.now)
		want, _ := time.Parse(time.RFC3339, c.want)
		if got := d.Next(now); !got.Equal(want) {
			t.Errorf("Next(%s) = %s, want %s", c.now, got.In(la), want)
		}
	}
}

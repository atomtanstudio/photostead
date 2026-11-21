package e2e

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/move"
	"photostead.com/server/internal/store/db"
)

func TestPairingAndMovePlan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	libDir := filepath.Join(t.TempDir(), "pairs")
	copyTree(t, "../testdata/pairs", libDir)
	// The file dates the move plan below relies on (the ones make-fixtures-pairs.sh sets).
	setModTimes(t, libDir, map[string]time.Time{
		"ai/render_00001.png":       fixtureTime, // no date inside: filed under 2025-01-02
		"ai/render_00001.mp4":       fixtureTime,
		"dated/2024-03-05/shot.png": fixtureTime, // later than the folder's date, so the folder wins
		"later/2026-01-01/late.png": fixtureTime, // earlier than the folder's date, so the file date wins
	})
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, "rich@example.test", adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, "rich@example.test")
	lib, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "pairs", Kind: "indexed", RootPath: libDir, ReadOnly: true, SidecarMode: "shadow"})
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
	data := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	la, _ := time.LoadLocation("America/Los_Angeles")
	rc, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{
		Logger: log, FetchPollInterval: 200 * time.Millisecond,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}, jobs.QueueImages: {MaxWorkers: 2}, jobs.QueueVideos: {MaxWorkers: 1}},
		Workers: jobs.Workers(&jobs.Deps{Store: s, Exif: exif, Render: media.Renderer{DataDir: data}, Zone: la, Log: log, TmpDir: t.TempDir()}),
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

	p, err := jobs.Pair(ctx, s.Q)
	if err != nil {
		t.Fatal(err)
	}
	if p.LivePhotosByName != 1 || p.LivePhotosByIdentifier != 0 || p.RawJpegStacks != 1 {
		t.Errorf("pairing = %+v, want 1 Live Photo by name and 1 RAW+JPEG stack (the AI image+video must not pair)", p)
	}
	if again, _ := jobs.Pair(ctx, s.Q); again != (jobs.Pairing{}) {
		t.Errorf("a second pass linked more: %+v", again)
	}

	// The clip and the RAW don't show on their own: 10 media files, 8 timeline items.
	buckets, err := s.Q.TimelineBuckets(ctx, db.TimelineBucketsParams{Viewer: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, b := range buckets {
		total += int(b.Count)
	}
	if total != 8 {
		t.Errorf("timeline items = %d, want 8", total)
	}

	res, err := move.MakePlan(ctx, s, owner, data)
	if err != nil {
		t.Fatal(err)
	}
	dst := map[string]string{}
	for _, e := range res.Entries {
		dst[e.Src] = e.Dst
	}
	want := map[string]string{
		"phone/IMG_0001.JPG":        "library/rich/photos/2022/2022-01-02/IMG_0001.JPG",
		"phone/IMG_0001.MOV":        "library/rich/photos/2022/2022-01-02/IMG_0001.MOV",
		"cam/DSC_0001.JPG":          "library/rich/photos/2021/2021-06-06/DSC_0001.JPG",
		"cam/DSC_0001.DNG":          "library/rich/photos/2021/2021-06-06/DSC_0001.DNG",
		"ai/render_00001.png":       "library/rich/photos/2025/2025-01-02/render_00001.png",
		"ai/render_00001.mp4":       "library/rich/videos/2025/2025-01-02/render_00001.mp4",
		"a/beach.jpg":               "library/rich/photos/2024/2024-05-01/beach.jpg",
		"b/Beach.JPG":               "library/rich/photos/2024/2024-05-01/Beach-2.JPG",
		"a/beach.jpg.xmp":           "library/rich/photos/2024/2024-05-01/beach.jpg.xmp",
		"dated/2024-03-05/shot.png": "library/rich/photos/2024/2024-03-05/shot.png", // the folder's earlier date
		"later/2026-01-01/late.png": "library/rich/photos/2025/2025-01-02/late.png", // a later name date is ignored
	}
	for src, d := range want {
		if dst[src] != d {
			t.Errorf("%s -> %q, want %q", src, dst[src], d)
		}
	}
	if len(res.Entries) != len(want) {
		t.Errorf("%d planned copies, want %d", len(res.Entries), len(want))
	}
	var saved int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM move_plan_entries WHERE plan_id = $1`, res.PlanID).Scan(&saved)
	if saved != len(res.Entries) {
		t.Errorf("saved %d entries, planned %d", saved, len(res.Entries))
	}
	md, err := os.ReadFile(filepath.Join(res.Dir, "summary.md"))
	if err != nil || !strings.Contains(string(md), "Nothing has been copied or moved") {
		t.Errorf("summary.md: %v", err)
	}
	if csv, err := os.ReadFile(filepath.Join(res.Dir, "plan.csv")); err != nil || strings.Count(string(csv), "\n") != len(want)+1 {
		t.Errorf("plan.csv: %v", err)
	}
	// Carry out the plan into a stand-in for the managed share.
	share := t.TempDir()
	before := snapshot(t, libDir)
	cp := move.Copier{Store: s, Target: share, ManagedRoot: share, Workers: 2}
	pilot, err := cp.Run(ctx, res.PlanID, 3, 0)
	if err != nil || pilot.Done != 3 {
		t.Fatalf("pilot run: %+v, %v", pilot, err)
	}
	rest, err := cp.Run(ctx, res.PlanID, 0, 0)
	if err != nil || rest.Failed != 0 || pilot.Done+rest.Done != len(want) {
		t.Fatalf("full run: %+v, %v", rest, err)
	}
	for _, e := range res.Entries {
		srcInfo, _ := os.Stat(filepath.Join(libDir, e.Src))
		dstInfo, err := os.Stat(filepath.Join(share, e.Dst))
		if err != nil {
			t.Errorf("%s not copied: %v", e.Dst, err)
			continue
		}
		a, _ := os.ReadFile(filepath.Join(libDir, e.Src))
		b, _ := os.ReadFile(filepath.Join(share, e.Dst))
		if string(a) != string(b) || !dstInfo.ModTime().Equal(srcInfo.ModTime()) {
			t.Errorf("%s: copy differs from the original (bytes or time)", e.Dst)
		}
	}
	if after := snapshot(t, libDir); after != before {
		t.Error("the move changed the original library")
	}
	var managed, servedFromShare int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM files f JOIN libraries l ON l.id = f.library_id WHERE l.kind = 'managed' AND f.state = 'present'`).Scan(&managed)
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets a JOIN files f ON f.id = a.primary_file_id JOIN libraries l ON l.id = f.library_id WHERE l.kind = 'managed'`).Scan(&servedFromShare)
	if managed != len(want) || servedFromShare != 9 {
		t.Errorf("managed files = %d (want %d), items served from the share = %d (want 9: every item except the Live clip)", managed, len(want), servedFromShare)
	}
	var root string
	_ = s.Pool.QueryRow(ctx, `SELECT root_path FROM libraries WHERE kind = 'managed'`).Scan(&root)
	if root != filepath.Join(share, "library", "rich") {
		t.Errorf("managed library root = %s", root)
	}
	// Running again finds nothing to do; a re-queued entry whose copy is already in place is
	// accepted; a different file at a destination is never overwritten.
	if again, err := cp.Run(ctx, res.PlanID, 0, 0); err != nil || again.Done+again.Failed != 0 {
		t.Errorf("second run did something: %+v, %v", again, err)
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE move_plan_entries SET state = 'pending' WHERE plan_id = $1 AND src_rel IN ('a/beach.jpg', 'b/Beach.JPG')`, res.PlanID)
	clash := filepath.Join(share, "library/rich/photos/2024/2024-05-01/Beach-2.JPG")
	if err := os.WriteFile(clash, []byte("someone else's file"), 0o644); err != nil {
		t.Fatal(err)
	}
	redo, err := cp.Run(ctx, res.PlanID, 0, 0)
	if err != nil || redo.Done != 1 || redo.Failed != 1 {
		t.Errorf("re-run = %+v, %v; want the identical copy accepted and the clash refused", redo, err)
	}
	if b, _ := os.ReadFile(clash); string(b) != "someone else's file" {
		t.Error("a different file at the destination was overwritten")
	}
	partials, _ := filepath.Glob(filepath.Join(share, "library/rich/*/*/*/*.photostead-partial"))
	if len(partials) != 0 {
		t.Errorf("partial files left behind: %v", partials)
	}

	// The dry run must not have written anything into the library.
	for _, f := range []string{"phone", "cam", "ai", "a", "b", "dated", "later"} {
		entries, _ := os.ReadDir(filepath.Join(libDir, f))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "library") {
				t.Errorf("dry run wrote %s/%s", f, e.Name())
			}
		}
	}
}

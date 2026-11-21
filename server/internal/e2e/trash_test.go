package e2e

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
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
	"photostead.com/server/internal/move"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/trash"
)

// Trash, restore and deleting for good, after the library has moved into a (stand-in) managed
// share: Photostead deletes only its own copy, the read-only original stays, and a rescan of the
// original's folder never brings the item back. Favorites and albums ride along.
func TestTrashFavoritesAlbums(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	libDir := filepath.Join(t.TempDir(), "pairs")
	copyTree(t, "../testdata/pairs", libDir)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
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
	render := media.Renderer{DataDir: data}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rc, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{
		Logger: log, FetchPollInterval: 200 * time.Millisecond,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}, jobs.QueueImages: {MaxWorkers: 2}, jobs.QueueVideos: {MaxWorkers: 1}},
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
	if _, err := jobs.Pair(ctx, s.Q); err != nil {
		t.Fatal(err)
	}
	plan, err := move.MakePlan(ctx, s, owner, data)
	if err != nil {
		t.Fatal(err)
	}
	share := t.TempDir()
	if _, err := (move.Copier{Store: s, Target: share, ManagedRoot: share, Workers: 2}).Run(ctx, plan.PlanID, 0, 0); err != nil {
		t.Fatal(err)
	}
	original := snapshot(t, libDir)

	h, _ := api.Handler(api.Options{Store: s, Render: render})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := srv.Client()
	c.Jar = jar
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	counts := func() api.Counts {
		var n api.Counts
		getJSON(t, c, srv.URL+"/api/v1/library/counts", &n)
		return n
	}
	start := counts()

	// A Live Photo goes to the trash with its clip, and comes back with it.
	var still, clip uuid.UUID
	_ = s.Pool.QueryRow(ctx, `SELECT id, live_motion_id FROM assets WHERE live_motion_id IS NOT NULL`).Scan(&still, &clip)
	var changed struct{ Changed int }

	// Archived with its clip, out of the timeline into the archive, and back.
	if code := send(c, srv.URL, "POST", "/api/v1/assets/archive", `{"assetIds":["`+still.String()+`"],"archived":true}`, &changed); code != 200 || changed.Changed != 2 {
		t.Fatalf("archive = %d %+v, want the still and its clip", code, changed)
	}
	if n := counts(); n.All != start.All-1 || n.Archived != start.Archived+1 {
		t.Errorf("after archiving: %d in the timeline (was %d), %d archived (was %d)", n.All, start.All, n.Archived, start.Archived)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/assets/archive", `{"assetIds":["`+still.String()+`"],"archived":true}`, &changed); code != 200 || changed.Changed != 0 {
		t.Errorf("archiving it again = %d %+v", code, changed)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/assets/archive", `{"assetIds":["`+still.String()+`"],"archived":false}`, &changed); code != 200 || changed.Changed != 2 {
		t.Errorf("back = %d %+v", code, changed)
	}
	if n := counts(); n.All != start.All || n.Archived != start.Archived {
		t.Errorf("after bringing it back: %d in the timeline (was %d), %d archived", n.All, start.All, n.Archived)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/assets/trash", `{"assetIds":["`+still.String()+`"]}`, &changed); code != 200 || changed.Changed != 2 {
		t.Fatalf("trash = %d %+v, want the still and its clip", code, changed)
	}
	if n := counts(); n.All != start.All-1 || n.Trash != 1 {
		t.Errorf("after trashing: %d in the timeline (was %d), %d in the trash", n.All, start.All, n.Trash)
	}
	var inTrash struct{ Total int }
	getJSON(t, c, srv.URL+"/api/v1/timeline/buckets?collection=trash", &inTrash)
	var detail api.Asset
	getJSON(t, c, srv.URL+"/api/v1/assets/"+still.String(), &detail)
	if inTrash.Total != 1 || !detail.Trashed {
		t.Errorf("trash view %d items; the item says trashed = %v", inTrash.Total, detail.Trashed)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/assets/restore", `{"assetIds":["`+still.String()+`"]}`, &changed); code != 200 || changed.Changed != 2 {
		t.Errorf("restore = %d %+v", code, changed)
	}
	if n := counts(); n.All != start.All || n.Trash != 0 {
		t.Errorf("after restoring: %+v", n)
	}

	// Deleting for good removes Photostead's copy and renders, never the read-only original, and
	// a rescan of the original's folder doesn't bring the item back.
	var gone uuid.UUID
	var copyPath string
	_ = s.Pool.QueryRow(ctx, `SELECT a.id, l.root_path || '/' || f.rel_path FROM assets a JOIN files f ON f.id = a.primary_file_id
		JOIN libraries l ON l.id = f.library_id WHERE l.kind = 'managed' AND a.type = 'image' AND a.live_motion_id IS NULL
		AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) LIMIT 1`).Scan(&gone, &copyPath)
	thumb, _ := render.Paths(gone)
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("the managed copy isn't there to start with: %v", err)
	}
	send(c, srv.URL, "POST", "/api/v1/assets/trash", `{"assetIds":["`+gone.String()+`"]}`, nil)
	var emptied struct{ Deleted, Kept int }
	if code := send(c, srv.URL, "POST", "/api/v1/trash/empty", `{"assetIds":["`+gone.String()+`"]}`, &emptied); code != 200 || emptied.Deleted != 1 || emptied.Kept != 0 {
		t.Fatalf("empty = %d %+v", code, emptied)
	}
	if _, err := os.Stat(copyPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the managed copy is still there: %v", err)
	}
	if _, err := os.Stat(thumb); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the thumbnail is still there: %v", err)
	}
	if snapshot(t, libDir) != original {
		t.Error("deleting changed the read-only original folder")
	}
	scan()
	var purged bool
	_ = s.Pool.QueryRow(ctx, `SELECT purged_at IS NOT NULL FROM assets WHERE id = $1`, gone).Scan(&purged)
	if n := counts(); !purged || n.All != start.All-1 || n.Trash != 0 {
		t.Errorf("after deleting and rescanning: purged %v, %d in the timeline (want %d), %d in the trash", purged, n.All, start.All-1, n.Trash)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/assets/restore", `{"assetIds":["`+gone.String()+`"]}`, &changed); code != 200 || changed.Changed != 0 {
		t.Errorf("a deleted item came back: %d %+v", code, changed)
	}

	// After 30 days in the trash, items go on their own.
	var old uuid.UUID
	_ = s.Pool.QueryRow(ctx, `SELECT a.id FROM assets a WHERE a.trashed_at IS NULL AND a.motion_of IS NULL AND a.live_motion_id IS NULL
		AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) LIMIT 1`).Scan(&old)
	send(c, srv.URL, "POST", "/api/v1/assets/trash", `{"assetIds":["`+old.String()+`"]}`, nil)
	if st, err := trash.Expired(ctx, s, render, time.Now(), log); err != nil || st.Deleted != 0 {
		t.Errorf("a fresh trash was emptied early: %+v %v", st, err)
	}
	if st, err := trash.Expired(ctx, s, render, time.Now().Add(trash.After+time.Hour), log); err != nil || st.Deleted != 1 {
		t.Errorf("30 days later: %+v %v", st, err)
	}

	// Favorites and albums, for items the viewer can see.
	var a1, a2 uuid.UUID
	_ = s.Pool.QueryRow(ctx, `SELECT (array_agg(id ORDER BY id))[1], (array_agg(id ORDER BY id))[2] FROM assets WHERE trashed_at IS NULL AND motion_of IS NULL`).Scan(&a1, &a2)
	if code := send(c, srv.URL, "POST", "/api/v1/assets/favorite", `{"assetIds":["`+a1.String()+`","`+a2.String()+`"],"favorite":true}`, &changed); code != 200 || changed.Changed != 2 {
		t.Errorf("favorite = %d %+v", code, changed)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/assets/favorite", `{"assetIds":["`+gone.String()+`"],"favorite":true}`, &changed); code != 200 || changed.Changed != 0 {
		t.Errorf("a deleted item became a favorite: %+v", changed)
	}
	if n := counts(); n.Favorites != 2 {
		t.Errorf("favorites = %d", n.Favorites)
	}
	var album api.Album
	if code := send(c, srv.URL, "POST", "/api/v1/albums", `{"title":"Summer","assetIds":["`+a1.String()+`","`+a2.String()+`"]}`, &album); code != 201 || album.Count != 2 || album.Title != "Summer" {
		t.Fatalf("new album = %d %+v", code, album)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/albums/"+album.ID.String()+"/remove", `{"assetIds":["`+a1.String()+`"]}`, &album); code != 200 || album.Count != 1 {
		t.Errorf("remove from album = %d %+v", code, album)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/albums/"+album.ID.String()+"/add", `{"assetIds":["`+a1.String()+`","`+gone.String()+`"]}`, &album); code != 200 || album.Count != 2 {
		t.Errorf("add to album = %d %+v (a deleted item must not join)", code, album)
	}
}

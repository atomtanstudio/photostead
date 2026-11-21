package e2e

import (
	"context"
	"image"
	"image/draw"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"go.n16f.net/thumbhash"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/stacks"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// signedIn indexes the test photos into a fresh database and returns the store, the owner, the
// server's address and a client signed in as the owner.
func signedIn(t *testing.T, ctx context.Context) (*store.Store, db.User, string, *http.Client) {
	t.Helper()
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
	t.Cleanup(func() { exif.Close() })
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
	t.Cleanup(func() { _ = rc.Stop(context.Background()) })
	if _, err := rc.Insert(ctx, jobs.ScanLibraryArgs{LibraryID: lib.ID}, nil); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)
	h, _ := api.Handler(api.Options{Store: s, Render: render})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	return s, owner, srv.URL, c
}

// Stars, flags and color labels: the owner's own, set on many items at once, shown with each
// item, and used to narrow the timeline and collections.
func TestRatingsFlagsLabels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, _, url, c := signedIn(t, ctx)

	var me api.User
	if code := doJSON(t, c, "PATCH", url+"/api/v1/auth/me/settings", `{"organize":true}`, &me); code != 200 || !me.Settings.Organize {
		t.Errorf("turning ratings on = %d %+v", code, me.Settings)
	}
	getJSON(t, c, url+"/api/v1/auth/me", &me)
	if !me.Settings.Organize {
		t.Error("the setting didn't stick")
	}

	var all struct {
		Total   int
		Buckets []api.Bucket
	}
	getJSON(t, c, url+"/api/v1/timeline/buckets", &all)
	if all.Total < 4 {
		t.Fatalf("only %d items", all.Total)
	}
	var month struct{ Assets []api.TimelineAsset }
	getJSON(t, c, url+"/api/v1/timeline?month="+all.Buckets[0].Month, &month)
	var ids []uuid.UUID
	for _, b := range all.Buckets {
		getJSON(t, c, url+"/api/v1/timeline?month="+b.Month, &month)
		for _, a := range month.Assets {
			ids = append(ids, a.ID)
		}
	}
	a, b, d := ids[0], ids[1], ids[2]
	mark := func(body string, want int64) {
		t.Helper()
		var out struct{ Changed int64 }
		if code := doJSON(t, c, "POST", url+"/api/v1/assets/marks", body, &out); code != 200 || out.Changed != want {
			t.Errorf("marks %s = %d %+v, want %d changed", body, code, out, want)
		}
	}
	mark(mustJSON(map[string]any{"assetIds": []uuid.UUID{a, b}, "rating": 4}), 2)
	mark(mustJSON(map[string]any{"assetIds": []uuid.UUID{a}, "flag": "pick"}), 1)
	mark(mustJSON(map[string]any{"assetIds": []uuid.UUID{b}, "flag": "reject", "label": "red"}), 1)
	mark(mustJSON(map[string]any{"assetIds": []uuid.UUID{d}, "label": "blue"}), 1)
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/marks", mustJSON(map[string]any{"assetIds": []uuid.UUID{a}}), nil); code != 422 {
		t.Errorf("marks with nothing to set = %d", code)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/marks", mustJSON(map[string]any{"assetIds": []uuid.UUID{a}, "label": "orange"}), nil); code != 422 {
		t.Errorf("an unknown color = %d", code)
	}

	count := func(q string) int {
		t.Helper()
		var r struct{ Total int }
		getJSON(t, c, url+"/api/v1/timeline/buckets?"+q, &r)
		return r.Total
	}
	for q, want := range map[string]int{"rating=4": 2, "rating=5": 0, "flag=pick": 1, "flag=reject": 1, "flag=unrejected": all.Total - 1,
		"label=red": 1, "label=blue": 1, "rating=4&flag=unrejected": 1} {
		if got := count(q); got != want {
			t.Errorf("%s: %d items, want %d", q, got, want)
		}
	}
	var detail api.Asset
	getJSON(t, c, url+"/api/v1/assets/"+a.String(), &detail)
	if detail.Rating != 4 || detail.Flag != "pick" || detail.Label != "" {
		t.Errorf("item a = %d %q %q", detail.Rating, detail.Flag, detail.Label)
	}
	getJSON(t, c, url+"/api/v1/assets/"+b.String(), &detail)
	if detail.Rating != 4 || detail.Flag != "reject" || detail.Label != "red" {
		t.Errorf("item b = %d %q %q", detail.Rating, detail.Flag, detail.Label)
	}
	// Each item in a month shows its marks.
	marked := 0
	for _, bk := range all.Buckets {
		getJSON(t, c, url+"/api/v1/timeline?month="+bk.Month+"&rating=4", &month)
		for _, x := range month.Assets {
			if x.Rating == 4 {
				marked++
			}
		}
	}
	if marked != 2 {
		t.Errorf("%d items in the months show 4 stars, want 2", marked)
	}
	// Collections narrow the same way.
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/favorite", mustJSON(map[string]any{"assetIds": []uuid.UUID{a, b}, "favorite": true}), nil); code != 200 {
		t.Fatalf("favorite = %d", code)
	}
	if got := count("collection=favorites&flag=pick"); got != 1 {
		t.Errorf("favorite picks = %d, want 1", got)
	}
	// Taking marks off.
	mark(mustJSON(map[string]any{"assetIds": []uuid.UUID{a, b}, "rating": 0, "flag": "none", "label": "none"}), 2)
	if got := count("rating=1"); got != 0 {
		t.Errorf("after clearing, %d rated items", got)
	}
	if got := count("label=blue"); got != 1 {
		t.Errorf("clearing a and b took d's label too: %d", got)
	}
}

// Editing many items at once: keywords on and off (names matched ignoring case), a description,
// and moving items in time.
func TestBulkEdits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, url, c := signedIn(t, ctx)
	var ids []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT id FROM assets WHERE motion_of IS NULL AND visibility = 'timeline' ORDER BY taken_at LIMIT 3`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) < 3 {
		t.Fatalf("%d items", len(ids))
	}
	a, b, d := ids[0], ids[1], ids[2]

	var kw struct{ Added, Removed int64 }
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/keywords", mustJSON(map[string]any{"assetIds": []uuid.UUID{a, b}, "add": []string{"Beach", " sunset "}}), &kw); code != 200 || kw.Added != 4 {
		t.Errorf("add keywords = %d %+v", code, kw)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/keywords", mustJSON(map[string]any{"assetIds": []uuid.UUID{d}, "add": []string{"beach"}}), &kw); code != 200 || kw.Added != 1 {
		t.Errorf("add an existing keyword in other case = %d %+v", code, kw)
	}
	var list struct{ Keywords []api.Keyword }
	getJSON(t, c, url+"/api/v1/keywords", &list)
	if len(list.Keywords) != 2 || list.Keywords[0].Name != "Beach" || list.Keywords[0].Items != 3 {
		t.Errorf("keywords = %+v", list.Keywords)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/keywords", mustJSON(map[string]any{"assetIds": []uuid.UUID{a}, "remove": []string{"BEACH"}}), &kw); code != 200 || kw.Removed != 1 {
		t.Errorf("remove = %d %+v", code, kw)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/keywords/list", mustJSON(map[string]any{"assetIds": []uuid.UUID{a, b}}), &list); code != 200 ||
		len(list.Keywords) != 2 || list.Keywords[0].Name != "sunset" || list.Keywords[0].Items != 2 || list.Keywords[1].Items != 1 {
		t.Errorf("selection's keywords = %d %+v", code, list.Keywords)
	}
	var detail api.Asset
	getJSON(t, c, url+"/api/v1/assets/"+a.String(), &detail)
	if len(detail.Keywords) != 1 || detail.Keywords[0] != "sunset" {
		t.Errorf("a's keywords = %v", detail.Keywords)
	}

	var changed struct{ Changed int64 }
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/description", mustJSON(map[string]any{"assetIds": []uuid.UUID{a, b}, "text": " Low tide "}), &changed); code != 200 || changed.Changed != 2 {
		t.Errorf("description = %d %+v", code, changed)
	}
	getJSON(t, c, url+"/api/v1/assets/"+a.String(), &detail)
	if detail.Description != "Low tide" {
		t.Errorf("a's description = %q", detail.Description)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/description", mustJSON(map[string]any{"assetIds": []uuid.UUID{a}, "text": ""}), nil); code != 200 {
		t.Errorf("emptying the description = %d", code)
	}
	var emptied api.Asset
	getJSON(t, c, url+"/api/v1/assets/"+a.String(), &emptied)
	if emptied.Description != "" {
		t.Errorf("an emptied description shows %q", emptied.Description)
	}

	// A day later, then the earliest of a and b set to a moment, b keeping its distance from a.
	var before, beforeB, after api.Asset
	getJSON(t, c, url+"/api/v1/assets/"+a.String(), &before)
	getJSON(t, c, url+"/api/v1/assets/"+b.String(), &beforeB)
	var moved struct {
		Changed      int
		ShiftSeconds int64
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/date", mustJSON(map[string]any{"assetIds": []uuid.UUID{a}, "shiftSeconds": 86400}), &moved); code != 200 || moved.Changed < 1 || moved.ShiftSeconds != 86400 {
		t.Errorf("shift = %d %+v", code, moved)
	}
	getJSON(t, c, url+"/api/v1/assets/"+a.String(), &after)
	day, _ := time.Parse("2006-01-02", before.LocalDate)
	if after.LocalDate != day.AddDate(0, 0, 1).Format("2006-01-02") || after.TakenAt.Sub(before.TakenAt) != 24*time.Hour || after.DateSource != "user" {
		t.Errorf("after a day: %s %v (%s), was %s %v", after.LocalDate, after.TakenAt, after.DateSource, before.LocalDate, before.TakenAt)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/date", mustJSON(map[string]any{"assetIds": []uuid.UUID{a, b}, "setEarliest": "2001-02-03T04:05"}), &moved); code != 200 || moved.Changed < 2 {
		t.Errorf("set earliest = %d %+v", code, moved)
	}
	var na, nb api.Asset
	getJSON(t, c, url+"/api/v1/assets/"+a.String(), &na)
	getJSON(t, c, url+"/api/v1/assets/"+b.String(), &nb)
	first := na
	if nb.TakenAt.Before(na.TakenAt) {
		first = nb
	}
	if first.LocalDate != "2001-02-03" {
		t.Errorf("the earliest is on %s, want 2001-02-03", first.LocalDate)
	}
	if gap, was := nb.TakenAt.Sub(na.TakenAt), beforeB.TakenAt.Sub(after.TakenAt); gap != was {
		t.Errorf("a and b were %v apart, now %v", was, gap)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/date", mustJSON(map[string]any{"assetIds": []uuid.UUID{a}, "setEarliest": "yesterday"}), nil); code != 422 {
		t.Errorf("a malformed date = %d", code)
	}
}

// Albums inside albums (moved, never into themselves, and lifted out when theirs is deleted),
// smart albums that follow their rules as items change, and the folders on disk.
func TestAlbumsSmartAlbumsFolders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, url, c := signedIn(t, ctx)
	var ids []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT id FROM assets WHERE motion_of IS NULL AND visibility = 'timeline' ORDER BY taken_at LIMIT 3`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	a, b := ids[0], ids[1]

	// Nesting.
	var trips, japan, kyoto api.Album
	if code := doJSON(t, c, "POST", url+"/api/v1/albums", `{"title":"Trips"}`, &trips); code != 201 {
		t.Fatalf("new album = %d", code)
	}
	_ = doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Japan", "parent": trips.ID, "assetIds": []uuid.UUID{a}}), &japan)
	_ = doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Kyoto", "parent": japan.ID}), &kyoto)
	if japan.Parent == nil || *japan.Parent != trips.ID || japan.Count != 1 {
		t.Errorf("Japan = %+v", japan)
	}
	var list struct{ Albums []api.Album }
	getJSON(t, c, url+"/api/v1/albums", &list)
	if len(list.Albums) != 1 || list.Albums[0].ID != trips.ID || list.Albums[0].Children != 1 {
		t.Errorf("top level = %+v", list.Albums)
	}
	getJSON(t, c, url+"/api/v1/albums?parent="+trips.ID.String(), &list)
	if len(list.Albums) != 1 || list.Albums[0].ID != japan.ID {
		t.Errorf("inside Trips = %+v", list.Albums)
	}
	getJSON(t, c, url+"/api/v1/albums?all=true", &list)
	for _, al := range list.Albums {
		if al.ID == kyoto.ID && (len(al.Path) != 2 || al.Path[0].Name != "Trips" || al.Path[1].Name != "Japan") {
			t.Errorf("Kyoto's path in the picker = %+v", al.Path)
		}
	}
	var one api.Album
	getJSON(t, c, url+"/api/v1/albums/"+kyoto.ID.String(), &one)
	if len(one.Path) != 2 || one.Path[1].ID != japan.ID {
		t.Errorf("Kyoto's path = %+v", one.Path)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/albums/"+trips.ID.String()+"/move", mustJSON(map[string]any{"parent": kyoto.ID}), nil); code != 422 {
		t.Errorf("Trips into its own Kyoto = %d, want 422", code)
	}
	var top api.Album
	if code := doJSON(t, c, "POST", url+"/api/v1/albums/"+kyoto.ID.String()+"/move", `{}`, &top); code != 200 || top.Parent != nil || len(top.Path) != 0 {
		t.Errorf("Kyoto to the top = %d %+v", code, top)
	}
	if code := doJSON(t, c, "PATCH", url+"/api/v1/albums/"+japan.ID.String(), `{"title":"Japan 2024","description":"Spring"}`, &one); code != 200 || one.Title != "Japan 2024" || one.Description != "Spring" {
		t.Errorf("rename = %d %+v", code, one)
	}
	_ = doJSON(t, c, "POST", url+"/api/v1/albums/"+kyoto.ID.String()+"/move", mustJSON(map[string]any{"parent": japan.ID}), nil)
	if code := status(t, c, "DELETE", url+"/api/v1/albums/"+japan.ID.String(), ""); code != 204 {
		t.Errorf("delete = %d", code)
	}
	getJSON(t, c, url+"/api/v1/albums/"+kyoto.ID.String(), &one)
	if one.Parent == nil || *one.Parent != trips.ID {
		t.Errorf("after deleting Japan, Kyoto is in %v, want Trips", one.Parent)
	}
	var detail api.Asset
	getJSON(t, c, url+"/api/v1/assets/"+a.String(), &detail)
	if detail.ID != a {
		t.Error("deleting an album took its item")
	}

	// A smart album: favorites with at least 3 stars. It follows as items change, and can't be added to by hand.
	var smart api.Album
	if code := doJSON(t, c, "POST", url+"/api/v1/albums", `{"title":"Best","rules":{"favorite":true,"rating":3}}`, &smart); code != 201 || smart.Kind != "smart" || smart.Count != 0 {
		t.Fatalf("smart album = %d %+v", code, smart)
	}
	_ = doJSON(t, c, "POST", url+"/api/v1/assets/favorite", mustJSON(map[string]any{"assetIds": []uuid.UUID{a, b}, "favorite": true}), nil)
	_ = doJSON(t, c, "POST", url+"/api/v1/assets/marks", mustJSON(map[string]any{"assetIds": []uuid.UUID{a}, "rating": 4}), nil)
	getJSON(t, c, url+"/api/v1/albums/"+smart.ID.String(), &one)
	if one.Count != 1 || one.Cover == nil || *one.Cover != a {
		t.Errorf("smart album after marking = %+v", one)
	}
	var buckets struct{ Total int }
	getJSON(t, c, url+"/api/v1/timeline/buckets?collection=album:"+smart.ID.String(), &buckets)
	if buckets.Total != 1 {
		t.Errorf("smart album's timeline = %d", buckets.Total)
	}
	_ = doJSON(t, c, "POST", url+"/api/v1/albums/"+smart.ID.String()+"/add", mustJSON(map[string]any{"assetIds": []uuid.UUID{b}}), nil)
	getJSON(t, c, url+"/api/v1/timeline/buckets?collection=album:"+smart.ID.String(), &buckets)
	if buckets.Total != 1 {
		t.Errorf("adding by hand changed a smart album: %d", buckets.Total)
	}
	if code := doJSON(t, c, "PATCH", url+"/api/v1/albums/"+smart.ID.String(), `{"rules":{"favorite":true}}`, &one); code != 200 || one.Count != 2 {
		t.Errorf("loosened rules = %d %+v", code, one)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/albums", `{"title":"Bad","rules":{"rating":9}}`, nil); code != 422 {
		t.Errorf("a rule out of range = %d", code)
	}

	// Folders: the library's top, a folder in it, and the items directly in a folder.
	var libs struct{ Libraries []api.Library }
	getJSON(t, c, url+"/api/v1/folders", &libs)
	if len(libs.Libraries) != 1 || libs.Libraries[0].Items == 0 {
		t.Fatalf("libraries = %+v", libs.Libraries)
	}
	lib := libs.Libraries[0].ID
	var folder struct {
		Path    string
		Folders []api.Folder
	}
	getJSON(t, c, url+"/api/v1/folders/"+lib.String(), &folder)
	names := map[string]int32{}
	for _, f := range folder.Folders {
		names[f.Name] = f.Items
	}
	if names["a"] != 1 || names["dated"] != 1 || len(folder.Folders) < 5 {
		t.Errorf("top folders = %+v", folder.Folders)
	}
	getJSON(t, c, url+"/api/v1/folders/"+lib.String()+"?path=dated", &folder)
	if folder.Path != "dated" || len(folder.Folders) != 1 || folder.Folders[0].Path != "dated/2024-03-05" {
		t.Errorf("dated = %+v", folder)
	}
	getJSON(t, c, url+"/api/v1/timeline/buckets?collection=folder:"+lib.String()+"&folder=a", &buckets)
	if buckets.Total != 1 {
		t.Errorf("items in a = %d", buckets.Total)
	}
	getJSON(t, c, url+"/api/v1/timeline/buckets?collection=folder:"+lib.String()+"&folder=dated", &buckets)
	if buckets.Total != 0 {
		t.Errorf("items directly in dated = %d (they're in its subfolder)", buckets.Total)
	}
	if code := status(t, c, "GET", url+"/api/v1/folders/"+lib.String()+"?path=../etc", ""); code != 422 {
		t.Errorf("climbing out of a library = %d", code)
	}
	if code := status(t, c, "GET", url+"/api/v1/folders/"+uuid.NewString(), ""); code != 404 {
		t.Errorf("someone else's library = %d", code)
	}
}

// Stacks: an AI batch and a camera burst stacked on their own, look-alikes suggested and settled,
// a stack made by hand, and the owner's changes kept through later passes. Trashing a batch's top
// item keeps the rest of the batch.
func TestStacks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, owner, url, c := signedIn(t, ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// An AI image made from a prompt (as provenance would record it).
	var ai, prompt uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT f.asset_id FROM files f WHERE f.rel_path = 'ai/render_00001.png'`).Scan(&ai); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `INSERT INTO prompts (owner_id, text, normalized_hash) VALUES ($1, 'a lighthouse', '\x01') RETURNING id`, owner.ID).Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM generations WHERE asset_id = $1`, ai)
	exec(`INSERT INTO generations (asset_id, kind, source, evidence, prompt_id, parser_version) VALUES ($1, 'generated', 'comfyui', 'png:prompt', $2, 1)`, ai, prompt)
	exec(`UPDATE assets SET source_type = 'ai_generated' WHERE id = $1`, ai)
	var others []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT a.id FROM assets a WHERE a.type = 'image' AND a.motion_of IS NULL AND a.visibility = 'timeline'
		AND a.id <> $1 AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) ORDER BY a.taken_at LIMIT 7`, ai)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		others = append(others, id)
	}
	rows.Close()
	if len(others) < 7 {
		t.Fatalf("%d other photos", len(others))
	}
	// Two more images made from the same prompt and model, half a minute and a minute and a half later.
	for i, id := range others[:2] {
		exec(`INSERT INTO generations (asset_id, kind, source, evidence, prompt_id, model_id, parser_version)
			SELECT $1, kind, source, evidence, prompt_id, model_id, parser_version FROM generations WHERE asset_id = $2`, id, ai)
		exec(`UPDATE assets SET source_type = 'ai_generated', taken_at = (SELECT taken_at FROM assets WHERE id = $2) + make_interval(secs => $3) WHERE id = $1`,
			id, ai, float64(30+60*i))
	}
	// A burst: three shots from one camera, half a second apart.
	base := time.Date(2019, 7, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range others[2:5] {
		exec(`UPDATE asset_metadata SET make = 'Canon', model = 'R5' WHERE asset_id = $1`, id)
		exec(`UPDATE assets SET taken_at = $2 WHERE id = $1`, id, base.Add(time.Duration(i)*500*time.Millisecond))
	}
	var before struct{ Total int }
	getJSON(t, c, url+"/api/v1/timeline/buckets", &before)

	st, err := stacks.Run(ctx, s, nil)
	if err != nil || st.Batches != 1 || st.BatchItems != 3 || st.Bursts != 1 || st.BurstItems != 3 {
		t.Fatalf("stacking = %+v %v", st, err)
	}
	var after struct{ Total int }
	getJSON(t, c, url+"/api/v1/timeline/buckets", &after)
	if after.Total != before.Total-4 {
		t.Errorf("timeline = %d, want %d (two batch and two burst items tucked under their tops)", after.Total, before.Total-4)
	}
	var batch api.Stack
	getJSON(t, c, url+"/api/v1/assets/"+others[1].String()+"/stack", &batch)
	if batch.Kind != "ai_batch" || len(batch.Members) != 3 || batch.State != "confirmed" {
		t.Fatalf("batch = %+v", batch)
	}
	// Trashing the top keeps the rest, with a new top.
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/trash", mustJSON(map[string]any{"assetIds": []uuid.UUID{batch.Cover}}), nil); code != 200 {
		t.Fatalf("trash = %d", code)
	}
	var left api.Stack
	getJSON(t, c, url+"/api/v1/assets/"+others[1].String()+"/stack", &left)
	if left.Cover == batch.Cover || len(left.Members) != 2 {
		t.Errorf("after trashing the top = %+v", left)
	}
	var trashedCount int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE trashed_at IS NOT NULL`).Scan(&trashedCount)
	if trashedCount != 1 {
		t.Errorf("%d items went to the trash, want just the top", trashedCount)
	}
	_ = doJSON(t, c, "POST", url+"/api/v1/assets/restore", mustJSON(map[string]any{"assetIds": []uuid.UUID{batch.Cover}}), nil)

	// Taken out, it stays out; unstacked, they stay apart.
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks/"+batch.ID.String()+"/remove", mustJSON(map[string]any{"assetIds": []uuid.UUID{others[1]}}), nil); code != 200 {
		t.Errorf("take out = %d", code)
	}
	if st, _ = stacks.Run(ctx, s, nil); st.BatchItems != 0 {
		t.Errorf("a pass put a taken-out item back: %+v", st)
	}
	var burst api.Stack
	getJSON(t, c, url+"/api/v1/assets/"+others[3].String()+"/stack", &burst)
	if burst.Kind != "burst" || len(burst.Members) != 3 {
		t.Fatalf("burst = %+v", burst)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks/"+burst.ID.String()+"/cover", mustJSON(map[string]any{"assetId": others[4]}), &burst); code != 200 || burst.Cover != others[4] {
		t.Errorf("new top = %d %+v", code, burst.Cover)
	}
	if code := status(t, c, "DELETE", url+"/api/v1/stacks/"+burst.ID.String(), ""); code != 204 {
		t.Errorf("unstack = %d", code)
	}
	if st, _ = stacks.Run(ctx, s, nil); st.Bursts != 0 || st.BurstItems != 0 {
		t.Errorf("a pass re-stacked an unstacked burst: %+v", st)
	}

	// Look-alikes: two photos that look the same are suggested; a third that doesn't isn't.
	vec := func(k int) string {
		v := make([]string, 768)
		for i := range v {
			v[i] = "0"
		}
		v[k] = "1"
		return "[" + strings.Join(v, ",") + "]"
	}
	exec(`DELETE FROM asset_embeddings`)
	for i, id := range others[5:7] {
		exec(`INSERT INTO asset_embeddings (asset_id, model, embedding) VALUES ($1, $2, $3::halfvec)`, id, search.Model, vec(1+0*i))
	}
	exec(`INSERT INTO asset_embeddings (asset_id, model, embedding) VALUES ($1, $2, $3::halfvec)`, others[2], search.Model, vec(5))
	// Blank (a black frame, say), the same two aren't look-alikes: blank pictures all look the same.
	kept := map[uuid.UUID][]byte{}
	for _, id := range others[5:7] {
		var h []byte
		_ = s.Pool.QueryRow(ctx, `SELECT thumbhash FROM assets WHERE id = $1`, id).Scan(&h)
		kept[id] = h
	}
	black := image.NewRGBA(image.Rect(0, 0, 32, 32))
	draw.Draw(black, black.Bounds(), image.Black, image.Point{}, draw.Src)
	exec(`UPDATE assets SET thumbhash = $2 WHERE id = ANY($1)`, others[5:7], thumbhash.EncodeImage(black))
	if st, err = stacks.NearDuplicates(ctx, s, 100, nil); err != nil || st.Checked != 3 || st.Looks != 0 {
		t.Fatalf("blank look-alikes = %+v %v", st, err)
	}
	for id, h := range kept {
		exec(`UPDATE assets SET thumbhash = $2 WHERE id = $1`, id, h)
	}
	exec(`DELETE FROM near_duplicate_checks`)
	if st, err = stacks.NearDuplicates(ctx, s, 100, nil); err != nil || st.Checked != 3 || st.Looks != 1 || st.LookItems != 2 {
		t.Fatalf("look-alikes = %+v %v", st, err)
	}
	var sugg struct {
		Total  int32
		Stacks []api.Stack
	}
	getJSON(t, c, url+"/api/v1/stacks/suggested", &sugg)
	if sugg.Total != 1 || len(sugg.Stacks) != 1 || len(sugg.Stacks[0].Members) != 2 {
		t.Fatalf("suggested = %+v", sugg)
	}
	var counts api.Counts
	getJSON(t, c, url+"/api/v1/library/counts", &counts)
	if counts.LookAlikes != 1 {
		t.Errorf("counts say %d look-alike groups", counts.LookAlikes)
	}
	// Stack all, then undo: back to waiting.
	var all struct {
		Changed  int64
		StackIDs []uuid.UUID
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks/suggested/confirm", "", &all); code != 200 || all.Changed != 1 || len(all.StackIDs) != 1 {
		t.Errorf("stack all = %d %+v", code, all)
	}
	getJSON(t, c, url+"/api/v1/stacks/suggested", &sugg)
	if sugg.Total != 0 {
		t.Errorf("after stack all, %d still waiting", sugg.Total)
	}
	var undone struct{ Changed int64 }
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks/unconfirm", mustJSON(map[string]any{"stackIds": all.StackIDs}), &undone); code != 200 || undone.Changed != 1 {
		t.Errorf("undo stack all = %d %+v", code, undone)
	}
	getJSON(t, c, url+"/api/v1/stacks/suggested", &sugg)
	if sugg.Total != 1 {
		t.Errorf("after undo, %d waiting", sugg.Total)
	}
	// Suggested look-alikes stay in the timeline until confirmed.
	getJSON(t, c, url+"/api/v1/timeline/buckets", &after)
	beforeConfirm := after.Total
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks/"+sugg.Stacks[0].ID.String()+"/confirm", "", nil); code != 200 {
		t.Errorf("confirm = %d", code)
	}
	getJSON(t, c, url+"/api/v1/timeline/buckets", &after)
	if after.Total != beforeConfirm-1 {
		t.Errorf("confirming tucked %d items away, want 1", beforeConfirm-after.Total)
	}
	if st, _ = stacks.NearDuplicates(ctx, s, 100, nil); st.Checked != 0 {
		t.Errorf("a second pass compared %d items again", st.Checked)
	}
	// Keep the top, trash the rest.
	var changed struct{ Changed int64 }
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks/"+sugg.Stacks[0].ID.String()+"/trash-rest", "", &changed); code != 200 || changed.Changed != 1 {
		t.Errorf("trash the rest = %d %+v", code, changed)
	}

	// By hand: two items not in a stack; not again once they are.
	var made api.Stack
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks", mustJSON(map[string]any{"assetIds": []uuid.UUID{others[2], others[3]}}), &made); code != 201 || made.Kind != "manual" || len(made.Members) != 2 {
		t.Errorf("stack by hand = %d %+v", code, made)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks", mustJSON(map[string]any{"assetIds": []uuid.UUID{others[2], others[4]}}), nil); code != 409 {
		t.Errorf("stacking an item that's in a stack = %d, want 409", code)
	}
	_ = owner
}

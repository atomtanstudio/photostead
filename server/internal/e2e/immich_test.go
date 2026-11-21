package e2e

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/immich"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/store/db"
)

// fakeImmich answers the few reads the import makes, from a list of items it can change
// between runs, and fails the test if it's ever asked to change anything.
type fakeImmich struct {
	t       *testing.T
	mu      sync.Mutex
	key     string
	assets  []immich.Asset
	albums  map[string][]string // album id -> asset ids
	persons map[string][]string // person id -> asset ids
	names   map[string]string   // person id -> name ("" for unnamed)
	tags    map[string][]string // tag path -> asset ids
	tagRead bool                // the key has the tag.read permission
}

func (f *fakeImmich) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.URL.Path == "/api/server/version" {
		reply(map[string]int{"major": 3, "minor": 0, "patch": 1})
		return
	}
	if r.Header.Get("x-api-key") != f.key {
		w.WriteHeader(401)
		reply(map[string]string{"message": "Invalid API key"})
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/albums":
		reply([]map[string]any{{"id": "al-1", "albumName": "Beach days", "description": "Summer", "albumThumbnailAssetId": "im-beach2",
			"assetCount": len(f.albums["al-1"]), "createdAt": "2024-06-01T00:00:00Z"}})
	case r.Method == "GET" && r.URL.Path == "/api/tags":
		if !f.tagRead {
			w.WriteHeader(403)
			reply(map[string]string{"message": "Missing required permission: tag.read"})
			return
		}
		var ts []map[string]string
		for value := range f.tags {
			ts = append(ts, map[string]string{"id": "tag:" + value, "name": value[strings.LastIndex(value, "/")+1:], "value": value})
		}
		reply(ts)
	case r.Method == "GET" && r.URL.Path == "/api/people":
		var ps []map[string]any
		for id, name := range f.names {
			ps = append(ps, map[string]any{"id": id, "name": name, "birthDate": "1990-04-02", "isHidden": false})
		}
		reply(map[string]any{"people": ps, "hasNextPage": false, "total": len(ps)})
	case r.Method == "POST" && r.URL.Path == "/api/search/metadata":
		var q struct {
			Page       int      `json:"page"`
			Size       int      `json:"size"`
			Visibility string   `json:"visibility"`
			AlbumIDs   []string `json:"albumIds"`
			PersonIDs  []string `json:"personIds"`
			TagIDs     []string `json:"tagIds"`
			WithExif   bool     `json:"withExif"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var only []string
		if len(q.AlbumIDs) > 0 {
			only = f.albums[q.AlbumIDs[0]]
		}
		if len(q.PersonIDs) > 0 {
			only = f.persons[q.PersonIDs[0]]
		}
		if len(q.TagIDs) > 0 {
			only = f.tags[strings.TrimPrefix(q.TagIDs[0], "tag:")]
		}
		var items []immich.Asset
		for _, a := range f.assets {
			vis := q.Visibility
			if vis == "" {
				vis = "timeline"
			}
			if a.Visibility == vis && !a.IsTrashed && (only == nil || slices.Contains(only, a.ID)) {
				if !q.WithExif {
					a.ExifInfo = nil // as Immich: details only when asked for
				}
				items = append(items, a)
			}
		}
		// Two items a page, to exercise paging.
		start := (max(q.Page, 1) - 1) * 2
		end := min(start+2, len(items))
		var next *string
		if end < len(items) {
			n := "2"
			if q.Page > 1 {
				b, _ := json.Marshal(q.Page + 1)
				n = string(b)
			}
			next = &n
		}
		page := []immich.Asset{}
		if start < len(items) {
			page = items[start:end]
		}
		reply(map[string]any{"assets": map[string]any{"items": page, "nextPage": next}})
	default:
		f.t.Errorf("the import asked Immich for %s %s; it must only read", r.Method, r.URL.Path)
		w.WriteHeader(400)
	}
}

func (f *fakeImmich) set(id string, change func(a *immich.Asset)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.assets {
		if f.assets[i].ID == id {
			change(&f.assets[i])
		}
	}
}

func TestImmichImport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	libDir := filepath.Join(t.TempDir(), "images")
	copyTree(t, "../testdata/pairs", libDir)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
	lib, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "images", Kind: "indexed", RootPath: libDir, ReadOnly: true, SidecarMode: "shadow"})
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
	if _, err := rc.Insert(ctx, jobs.ScanLibraryArgs{LibraryID: lib.ID}, nil); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)
	if _, err := jobs.Pair(ctx, s.Q); err != nil {
		t.Fatal(err)
	}

	// Immich sees the same folder at its own path, and hashes the path (an external library).
	ext := func(id, rel, vis string, fav bool) immich.Asset {
		p := "/mnt/external/images/" + rel
		sum := sha1.Sum([]byte("path:" + p))
		typ := "IMAGE"
		if strings.HasSuffix(strings.ToLower(rel), ".mov") || strings.HasSuffix(rel, ".mp4") {
			typ = "VIDEO"
		}
		return immich.Asset{ID: id, Type: typ, OriginalPath: p, Checksum: base64.StdEncoding.EncodeToString(sum[:]), IsFavorite: fav, Visibility: vis}
	}
	trashed := ext("im-trash", "ai/render_00001.mp4", "timeline", true)
	trashed.IsTrashed = true
	fake := &fakeImmich{t: t, key: "test-key",
		assets: []immich.Asset{
			ext("im-beach", "a/beach.jpg", "timeline", true),
			ext("im-beach2", "b/Beach.JPG", "timeline", false),
			ext("im-dsc", "cam/DSC_0001.JPG", "archive", false),
			ext("im-render", "ai/render_00001.png", "timeline", false),
			ext("im-phone", "phone/IMG_0001.JPG", "timeline", false),
			ext("im-clip", "phone/IMG_0001.MOV", "hidden", true),    // a Live Photo's clip: never imported
			ext("im-gone", "lora-datasets/x.png", "timeline", true), // an excluded folder
			trashed,
		},
		albums:  map[string][]string{"al-1": {"im-beach", "im-beach2", "im-gone"}},
		persons: map[string][]string{"p-ada": {"im-phone", "im-render"}, "p-unnamed": {"im-dsc"}},
		names:   map[string]string{"p-ada": "Ada", "p-unnamed": ""},
		tags:    map[string][]string{"style/golden hour": {"im-beach", "im-gone"}, "portrait": {"im-phone"}},
	}
	describe := func(id, text string) {
		fake.set(id, func(a *immich.Asset) {
			if a.ExifInfo == nil {
				a.ExifInfo = &immich.ExifInfo{}
			}
			a.ExifInfo.Description = text
		})
	}
	describe("im-beach", "Sunset over the bay · @someone · https://x.com/someone/status/1")
	fake.set("im-phone", func(a *immich.Asset) { // rated in Immich
		five := 5
		if a.ExifInfo == nil {
			a.ExifInfo = &immich.ExifInfo{}
		}
		a.ExifInfo.Rating = &five
	})
	describe("im-render", "A long prompt about a lighthouse")
	imm := httptest.NewServer(fake)
	defer imm.Close()

	h, _ := api.Handler(api.Options{Store: s, Render: render, Jobs: rc})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	send := func(method, path, body string, out any) int {
		resp := do(t, c, method, srv.URL+path, body, nil)
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	var conn struct {
		Saved bool         `json:"saved"`
		Check immich.Check `json:"check"`
	}
	if code := send("PUT", "/api/v1/imports/immich", `{"url":"`+imm.URL+`/","apiKey":"wrong"}`, &conn); code != 200 || conn.Saved || !strings.Contains(conn.Check.Items, "didn't accept the key") {
		t.Errorf("wrong key = %d %+v", code, conn)
	}
	if code := send("POST", "/api/v1/imports/immich/run", `{}`, nil); code != 409 {
		t.Errorf("import before connecting = %d, want 409", code)
	}
	if code := send("PUT", "/api/v1/imports/immich", `{"url":"`+imm.URL+`","apiKey":"test-key"}`, &conn); code != 200 || !conn.Saved || conn.Check.Albums != "ok" {
		t.Fatalf("connect = %d %+v", code, conn)
	}
	var state api.ImmichImport
	getJSON(t, c, srv.URL+"/api/v1/imports/immich", &state)
	if !state.Connected || state.URL != imm.URL {
		t.Errorf("state = %+v", state)
	}
	if b, _ := json.Marshal(state); strings.Contains(string(b), "test-key") {
		t.Error("the API returned the Immich key")
	}

	runImport := func() immich.Summary {
		t.Helper()
		var started api.ImmichImport
		if code := send("POST", "/api/v1/imports/immich/run", `{}`, &started); code != 202 {
			t.Fatalf("run = %d", code)
		}
		if started.Run == nil || started.Run.State != "running" {
			t.Errorf("a requested import doesn't show as running: %+v", started.Run)
		}
		waitForJobs(t, ctx, s)
		getJSON(t, c, srv.URL+"/api/v1/imports/immich", &state)
		if state.Run == nil || state.Run.State != "done" {
			t.Fatalf("import = %+v", state.Run)
		}
		var sum immich.Summary
		_ = json.Unmarshal(state.Run.Summary, &sum)
		return sum
	}
	sum := runImport()
	if sum.Items != 6 || sum.Matched != 5 || sum.ByPath != 5 || sum.Unmatched != 1 || sum.Favorites != 1 || sum.Archived != 1 ||
		sum.Albums != 1 || sum.AlbumItems != 2 || sum.AlbumsMissing != 1 || sum.People != 1 || sum.PeopleItems != 2 ||
		sum.Descriptions != 2 || sum.DescriptionsSet != 2 || sum.Ratings != 1 || sum.RatingsSet != 1 || sum.Tags != 0 || !slices.ContainsFunc(sum.Skipped, func(s string) bool { return strings.Contains(s, "tag.read") }) {
		t.Errorf("summary = %+v", sum)
	}

	ids := map[string]string{} // relative path -> item id
	rows, _ := s.Pool.Query(ctx, `SELECT rel_path, asset_id FROM files WHERE asset_id IS NOT NULL`)
	for rows.Next() {
		var p, id string
		_ = rows.Scan(&p, &id)
		ids[p] = id
	}
	month := func(collection string) []string {
		var b struct {
			Buckets []struct{ Month string } `json:"buckets"`
		}
		getJSON(t, c, srv.URL+"/api/v1/timeline/buckets?collection="+collection, &b)
		var out []string
		for _, m := range b.Buckets {
			var r struct {
				Assets []struct {
					ID       string `json:"id"`
					Favorite bool   `json:"favorite"`
				} `json:"assets"`
			}
			getJSON(t, c, srv.URL+"/api/v1/timeline?month="+m.Month+"&collection="+collection, &r)
			for _, a := range r.Assets {
				for p, id := range ids {
					if id == a.ID {
						out = append(out, p)
					}
				}
			}
		}
		slices.Sort(out)
		return out
	}
	if got := month("favorites"); !slices.Equal(got, []string{"a/beach.jpg"}) {
		t.Errorf("favorites = %v", got)
	}
	if got := month("archive"); !slices.Equal(got, []string{"cam/DSC_0001.JPG"}) {
		t.Errorf("archive = %v", got)
	}
	var counts api.Counts
	getJSON(t, c, srv.URL+"/api/v1/library/counts", &counts)
	if counts.Favorites != 1 || counts.Archived != 1 || counts.Albums != 1 || counts.People != 1 {
		t.Errorf("counts = %+v", counts)
	}
	var albums struct{ Albums []api.Album }
	getJSON(t, c, srv.URL+"/api/v1/albums", &albums)
	if len(albums.Albums) != 1 || albums.Albums[0].Title != "Beach days" || albums.Albums[0].Count != 2 ||
		albums.Albums[0].Cover == nil || albums.Albums[0].Cover.String() != ids["b/Beach.JPG"] {
		t.Fatalf("albums = %+v", albums.Albums)
	}
	album := albums.Albums[0].ID.String()
	if got := month("album:" + album); !slices.Equal(got, []string{"a/beach.jpg", "b/Beach.JPG"}) {
		t.Errorf("album = %v", got)
	}
	var people struct{ People []api.Person }
	getJSON(t, c, srv.URL+"/api/v1/people", &people)
	if len(people.People) != 1 || people.People[0].Name != "Ada" || people.People[0].BirthDate != "1990-04-02" || people.People[0].Count != 2 {
		t.Fatalf("people = %+v", people.People)
	}
	if got := month("person:" + people.People[0].ID.String()); !slices.Equal(got, []string{"ai/render_00001.png", "phone/IMG_0001.JPG"}) {
		t.Errorf("Ada = %v", got)
	}
	var detail api.Asset
	getJSON(t, c, srv.URL+"/api/v1/assets/"+ids["a/beach.jpg"], &detail)
	if !detail.Favorite || len(detail.Albums) != 1 || detail.Albums[0].Name != "Beach days" {
		t.Errorf("detail = %+v", detail)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+ids["phone/IMG_0001.JPG"], &detail)
	if len(detail.People) != 1 || detail.People[0].Name != "Ada" || detail.People[0].Source != "immich" {
		t.Errorf("people on the phone photo = %+v", detail.People)
	}
	// Descriptions came over, and win over the file's own.
	getJSON(t, c, srv.URL+"/api/v1/assets/"+ids["a/beach.jpg"], &detail)
	if detail.Description != "Sunset over the bay · @someone · https://x.com/someone/status/1" || len(detail.Keywords) != 0 {
		t.Errorf("beach description %q, keywords %v", detail.Description, detail.Keywords)
	}
	// The archived item left the timeline.
	var tl struct{ Total int32 }
	getJSON(t, c, srv.URL+"/api/v1/timeline/buckets", &tl)
	if tl.Total != counts.All || counts.All != 7 {
		t.Errorf("timeline = %d, counts.all = %d, want 7 (8 less the archived one)", tl.Total, counts.All)
	}

	// Changes on both sides. In Immich: the beach photo is no longer a favorite, the render is,
	// the second beach photo leaves the album, and the archived photo comes back. Here: the phone
	// photo is made a favorite, which the next import must keep.
	fake.set("im-beach", func(a *immich.Asset) { a.IsFavorite = false })
	fake.set("im-render", func(a *immich.Asset) { a.IsFavorite = true })
	fake.set("im-dsc", func(a *immich.Asset) { a.Visibility = "timeline" })
	fake.mu.Lock()
	fake.albums["al-1"] = []string{"im-beach"}
	fake.mu.Unlock()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO asset_user_state (asset_id, user_id, favorite) VALUES ($1, $2, true)
		ON CONFLICT (asset_id, user_id) DO UPDATE SET favorite = true`, ids["phone/IMG_0001.JPG"], owner.ID); err != nil {
		t.Fatal(err)
	}
	sum = runImport()
	if sum.FavoritesSet != 1 || sum.FavoritesOff != 1 || sum.ArchivedOff != 1 || sum.AlbumsNew != 0 || sum.PeopleNew != 0 {
		t.Errorf("second summary = %+v", sum)
	}
	if got := month("favorites"); !slices.Equal(got, []string{"ai/render_00001.png", "phone/IMG_0001.JPG"}) {
		t.Errorf("favorites after the second import = %v", got)
	}
	if got := month("archive"); len(got) != 0 {
		t.Errorf("archive after the second import = %v", got)
	}
	if got := month("album:" + album); !slices.Equal(got, []string{"a/beach.jpg"}) {
		t.Errorf("album after the second import = %v", got)
	}
	getJSON(t, c, srv.URL+"/api/v1/albums", &albums)
	if len(albums.Albums) != 1 {
		t.Errorf("albums after the second import = %d, want the same one", len(albums.Albums))
	}

	// With tag.read, tags come over as keywords. A description changed in Immich follows; one
	// written here stays.
	fake.mu.Lock()
	fake.tagRead = true
	fake.mu.Unlock()
	describe("im-beach", "Sunset over the bay, edited in Immich")
	describe("im-render", "Changed in Immich")
	if _, err := s.Pool.Exec(ctx, `UPDATE asset_descriptions SET text = 'Mine', source = 'user' WHERE asset_id = $1`, ids["ai/render_00001.png"]); err != nil {
		t.Fatal(err)
	}
	sum = runImport()
	if sum.Tags != 2 || sum.TagItems != 2 || sum.DescriptionsSet != 1 || len(sum.Skipped) != 0 {
		t.Errorf("third summary = %+v", sum)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+ids["a/beach.jpg"], &detail)
	if detail.Description != "Sunset over the bay, edited in Immich" || !slices.Equal(detail.Keywords, []string{"style/golden hour"}) {
		t.Errorf("beach after tags: %q %v", detail.Description, detail.Keywords)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+ids["ai/render_00001.png"], &detail)
	if detail.Description != "Mine" {
		t.Errorf("a description written here was replaced: %q", detail.Description)
	}

	// Forgetting the connection keeps what was imported.
	if code := send("DELETE", "/api/v1/imports/immich", "", nil); code != 204 {
		t.Errorf("forget = %d", code)
	}
	getJSON(t, c, srv.URL+"/api/v1/imports/immich", &state)
	if state.Connected || state.Run == nil {
		t.Errorf("after forgetting = %+v", state)
	}
	getJSON(t, c, srv.URL+"/api/v1/library/counts", &counts)
	if counts.Favorites != 2 || counts.Albums != 1 {
		t.Errorf("counts after forgetting = %+v", counts)
	}
}

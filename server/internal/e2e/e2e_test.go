// Package e2e indexes a small read-only library into a throwaway database and checks the
// results through the database and the HTTP API. It needs Postgres (make db-up).
package e2e

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

const (
	adminEmail = "admin@example.test"
	adminPass  = "correct horse battery staple"
)

func TestIndexLibraryReadOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)

	// A read-only copy of the fixture library, with its listing and times recorded.
	libDir := filepath.Join(t.TempDir(), "library")
	copyTree(t, "../testdata/library", libDir)
	// The file dates the checks below rely on (the ones make-fixtures.sh sets; git doesn't keep them).
	setModTimes(t, libDir, map[string]time.Time{
		"generated.png":              fixtureTime,                                  // 2025-01-02 once its older copy is excluded
		"copies/generated-older.png": time.Date(2024, 3, 3, 11, 3, 3, 0, time.UTC), // the oldest copy dates the item: 2024-03-03
		"still.heic":                 fixtureTime,                                  // with only-here.png, the 2025-01 bucket
		"extra/only-here.png":        fixtureTime,
	})
	before := snapshot(t, libDir)
	makeReadOnly(t, libDir)
	t.Cleanup(func() { makeWritable(libDir) })

	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	admin, err := s.Q.GetUserByEmail(ctx, adminEmail)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: admin.ID, Name: "fixtures", Kind: "indexed",
		RootPath: libDir, ReadOnly: true, SidecarMode: "shadow"})
	if err != nil {
		t.Fatal(err)
	}

	// Run the real job pipeline.
	if err := media.Start(2); err != nil {
		t.Fatal(err)
	}
	exif, err := metadata.NewReader(2)
	if err != nil {
		t.Fatal(err)
	}
	defer exif.Close()
	render := media.Renderer{DataDir: t.TempDir()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rc, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{
		Logger:            log,
		Queues:            map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}, jobs.QueueImages: {MaxWorkers: 2}, jobs.QueueVideos: {MaxWorkers: 1}},
		Workers:           jobs.Workers(&jobs.Deps{Store: s, Exif: exif, Render: render, Zone: time.UTC, Log: log, TmpDir: t.TempDir()}),
		FetchPollInterval: 200 * time.Millisecond,
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

	t.Run("files", func(t *testing.T) {
		want := map[string]string{
			"photo-tokyo.jpg": "present", "copies/photo-tokyo-copy.jpg": "present", "generated.png": "present",
			"clip.mp4": "present", "still.heic": "present", "notes.txt": "skipped", "photo-tokyo.jpg.xmp": "skipped",
			"copies/generated-older.png": "present", "empty.jpg": "skipped", "extra/only-here.png": "present",
		}
		got := map[string]string{}
		rows, _ := s.Pool.Query(ctx, `SELECT rel_path, state FROM files`)
		for rows.Next() {
			var p, st string
			_ = rows.Scan(&p, &st)
			got[p] = st
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("files = %v\nwant    %v", got, want)
		}
	})

	t.Run("one asset per unique content", func(t *testing.T) {
		var assets, linked int
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets`).Scan(&assets)
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM files WHERE asset_id = (SELECT asset_id FROM files WHERE rel_path = 'photo-tokyo.jpg')`).Scan(&linked)
		if assets != 5 || linked != 2 {
			t.Errorf("assets = %d (want 5), files on the Tokyo photo = %d (want 2)", assets, linked)
		}
	})

	t.Run("metadata", func(t *testing.T) {
		type row struct {
			date, source, typ, render string
			offset                    *int16
			w, h, dur                 *int32
			make                      *string
			lat                       *float64
		}
		get := func(p string) row {
			var r row
			var d time.Time
			err := s.Pool.QueryRow(ctx, `SELECT a.local_date, a.date_source, a.type, a.render_state, a.taken_offset_min,
				a.width, a.height, a.duration_ms, m.make, m.lat
				FROM files f JOIN assets a ON a.id = f.asset_id JOIN asset_metadata m ON m.asset_id = a.id
				WHERE f.rel_path = $1`, p).Scan(&d, &r.source, &r.typ, &r.render, &r.offset, &r.w, &r.h, &r.dur, &r.make, &r.lat)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			r.date = d.Format("2006-01-02")
			return r
		}
		tokyo := get("photo-tokyo.jpg")
		if tokyo.date != "2024-05-01" || tokyo.source != "exif-original" || tokyo.offset == nil || *tokyo.offset != 540 {
			t.Errorf("tokyo date = %s from %s, offset %v", tokyo.date, tokyo.source, tokyo.offset)
		}
		if *tokyo.w != 800 || *tokyo.h != 1200 {
			t.Errorf("tokyo is %dx%d, want 800x1200 (rotated by EXIF orientation 6)", *tokyo.w, *tokyo.h)
		}
		if tokyo.make == nil || *tokyo.make != "Apple" || tokyo.lat == nil || *tokyo.lat < 35.6 {
			t.Errorf("tokyo camera/location = %v %v", tokyo.make, tokyo.lat)
		}
		clip := get("clip.mp4")
		if clip.typ != "video" || clip.source != "quicktime" || clip.date != "2023-08-12" || clip.dur == nil || *clip.dur < 1900 {
			t.Errorf("clip = %+v", clip)
		}
		png := get("generated.png")
		if png.source != "file-modified" || png.date != "2024-03-03" {
			t.Errorf("png date = %s from %s, want the oldest copy's file date 2024-03-03", png.date, png.source)
		}
		for _, p := range []string{"photo-tokyo.jpg", "generated.png", "clip.mp4", "still.heic"} {
			if r := get(p); r.render != "done" {
				t.Errorf("%s render = %s", p, r.render)
			}
		}
	})

	t.Run("originals untouched", func(t *testing.T) {
		makeWritable(libDir) // only so the snapshot can read everything
		defer makeReadOnly(t, libDir)
		if after := snapshot(t, libDir); after != before {
			t.Errorf("library changed:\nbefore %s\nafter  %s", before, after)
		}
	})

	t.Run("api", func(t *testing.T) {
		h, _ := api.Handler(api.Options{Store: s, Render: render})
		srv := httptest.NewServer(h)
		defer srv.Close()
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}

		if code := status(t, c, "GET", srv.URL+"/api/v1/timeline/buckets", ""); code != 401 {
			t.Errorf("buckets without a session = %d, want 401", code)
		}
		if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"wrong"}`); code != 401 {
			t.Errorf("bad password = %d, want 401", code)
		}
		if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
			t.Fatalf("login = %d", code)
		}
		var buckets struct {
			Total   int `json:"total"`
			Buckets []struct {
				Month string `json:"month"`
				Count int    `json:"count"`
			} `json:"buckets"`
		}
		getJSON(t, c, srv.URL+"/api/v1/timeline/buckets", &buckets)
		if buckets.Total != 5 || len(buckets.Buckets) != 4 || buckets.Buckets[0].Month != "2025-01" {
			t.Errorf("buckets = %+v", buckets)
		}
		var month struct {
			Assets []struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				Width     int    `json:"width"`
				ThumbHash string `json:"thumbhash"`
			} `json:"assets"`
		}
		getJSON(t, c, srv.URL+"/api/v1/timeline?month=2024-05", &month)
		if len(month.Assets) != 1 || month.Assets[0].Width != 800 || month.Assets[0].ThumbHash == "" {
			t.Fatalf("2024-05 = %+v", month)
		}
		id := month.Assets[0].ID
		resp := do(t, c, "GET", srv.URL+"/api/v1/assets/"+id+"/thumbnail", "", nil)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/webp" {
			t.Errorf("thumbnail = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		resp = do(t, c, "GET", srv.URL+"/api/v1/assets/"+id+"/original", "", map[string]string{"Range": "bytes=0-99"})
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 206 || len(body) != 100 {
			t.Errorf("original range = %d, %d bytes", resp.StatusCode, len(body))
		}
		var detail struct {
			FileName   string `json:"fileName"`
			DateSource string `json:"dateSource"`
			OffsetMin  int    `json:"offsetMin"`
			Camera     struct {
				Model string `json:"model"`
			} `json:"camera"`
		}
		getJSON(t, c, srv.URL+"/api/v1/assets/"+id, &detail)
		if !strings.HasPrefix(detail.FileName, "photo-tokyo") || detail.OffsetMin != 540 || detail.Camera.Model != "iPhone 15 Pro" {
			t.Errorf("detail = %+v", detail)
		}
		if code := status(t, c, "GET", srv.URL+"/api/v1/assets/00000000-0000-7000-8000-000000000000", ""); code != 404 {
			t.Errorf("unknown asset = %d, want 404", code)
		}

		// Filters, name search, zip downloads, status.
		var vids struct {
			Total int `json:"total"`
		}
		getJSON(t, c, srv.URL+"/api/v1/timeline/buckets?type=video", &vids)
		if vids.Total != 1 {
			t.Errorf("videos = %d, want 1", vids.Total)
		}
		type found struct {
			Assets []struct {
				ID string `json:"id"`
			} `json:"assets"`
		}
		for q, want := range map[string]int{"tokyo": 1, "TOKYO copy": 1, "clip mp4": 1, "nothing-like-this": 0, "100%": 0} {
			var f found
			getJSON(t, c, srv.URL+"/api/v1/search?q="+url.QueryEscape(q), &f)
			if len(f.Assets) != want {
				t.Errorf("search %q = %d items, want %d", q, len(f.Assets), want)
			}
		}
		var clipFound found
		getJSON(t, c, srv.URL+"/api/v1/search?q=clip", &clipFound)
		resp = do(t, c, "POST", srv.URL+"/api/v1/downloads", `{"ids":["`+id+`","`+clipFound.Assets[0].ID+`"]}`, nil)
		var dl struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&dl)
		resp = do(t, c, "GET", srv.URL+dl.URL, "", nil)
		zipped, _ := io.ReadAll(resp.Body)
		zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
		if resp.StatusCode != 200 || err != nil || len(zr.File) != 2 {
			t.Fatalf("download = %d, %v", resp.StatusCode, err)
		}
		names := zr.File[0].Name + " " + zr.File[1].Name
		if !strings.Contains(names, "clip.mp4") || !strings.Contains(names, "photo-tokyo") {
			t.Errorf("zip holds %s", names)
		}
		if code := status(t, c, "GET", srv.URL+"/api/v1/downloads/not-a-real-link", ""); code != 404 {
			t.Errorf("unknown download = %d, want 404", code)
		}
		var st struct {
			Indexing int `json:"indexing"`
		}
		getJSON(t, c, srv.URL+"/api/v1/status", &st)
		if st.Indexing != 0 {
			t.Errorf("status indexing = %d, want 0", st.Indexing)
		}

		// Choosing a time zone re-dates moments (the clip's UTC clock) but not recorded offsets.
		if code := status(t, c, "PUT", srv.URL+"/api/v1/auth/me/time-zone", `{"timeZone":"Mars/Olympus"}`); code != 422 {
			t.Errorf("unknown zone = %d, want 422", code)
		}
		if code := status(t, c, "PUT", srv.URL+"/api/v1/auth/me/time-zone", `{"timeZone":"Local"}`); code != 422 {
			t.Errorf("zone Local = %d, want 422", code)
		}
		resp = do(t, c, "PUT", srv.URL+"/api/v1/auth/me/time-zone", `{"timeZone":"Asia/Tokyo"}`, nil)
		var tz struct {
			User struct {
				TimeZone       string `json:"timeZone"`
				TimeZoneChosen bool   `json:"timeZoneChosen"`
			} `json:"user"`
			DaysChanged int `json:"daysChanged"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&tz)
		if resp.StatusCode != 200 || tz.User.TimeZone != "Asia/Tokyo" || !tz.User.TimeZoneChosen || tz.DaysChanged < 1 {
			t.Errorf("set zone = %d %+v", resp.StatusCode, tz)
		}
		day := func(p string) string {
			var d time.Time
			_ = s.Pool.QueryRow(ctx, `SELECT a.local_date FROM files f JOIN assets a ON a.id = f.asset_id WHERE f.rel_path = $1`, p).Scan(&d)
			return d.Format("2006-01-02")
		}
		if got := day("clip.mp4"); got != "2023-08-13" {
			t.Errorf("clip day in Tokyo = %s, want 2023-08-13 (18:00 UTC is 03:00 next day there)", got)
		}
		if got := day("photo-tokyo.jpg"); got != "2024-05-01" {
			t.Errorf("tokyo photo day = %s, want unchanged 2024-05-01", got)
		}
		if code := status(t, c, "POST", srv.URL+"/api/v1/setup", `{"displayName":"X","email":"x@example.test","password":"another long password","timeZone":"UTC"}`); code != 409 {
			t.Errorf("second setup = %d, want 409", code)
		}
	})

	t.Run("exclude folders", func(t *testing.T) {
		var onlyHere uuid.UUID
		_ = s.Pool.QueryRow(ctx, `SELECT asset_id FROM files WHERE rel_path = 'extra/only-here.png'`).Scan(&onlyHere)
		thumb, preview := render.Paths(onlyHere)
		if _, err := os.Stat(preview); err != nil {
			t.Fatalf("preview of the extra item missing before exclusion: %v", err)
		}
		for _, folder := range []string{"copies", "extra"} {
			if _, err := jobs.PruneFolder(ctx, s, render, "UTC", lib.ID, folder); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Q.SetLibraryExclude(ctx, db.SetLibraryExcludeParams{ID: lib.ID, Exclude: []string{"copies", "extra"}}); err != nil {
			t.Fatal(err)
		}
		count := func(q string) (n int) { _ = s.Pool.QueryRow(ctx, q).Scan(&n); return }
		if n := count(`SELECT count(*) FROM files WHERE rel_path LIKE 'copies/%' OR rel_path LIKE 'extra/%'`); n != 0 {
			t.Errorf("%d files from excluded folders still indexed", n)
		}
		if n := count(`SELECT count(*) FROM assets`); n != 4 {
			t.Errorf("assets = %d, want 4 (the extra item removed, the rest kept)", n)
		}
		if n := count(`SELECT count(*) FROM assets WHERE primary_file_id IS NULL`); n != 0 {
			t.Errorf("%d items lost their file", n)
		}
		for _, p := range []string{thumb, preview} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("%s should be deleted with its item", p)
			}
		}
		var d time.Time
		_ = s.Pool.QueryRow(ctx, `SELECT a.local_date FROM files f JOIN assets a ON a.id = f.asset_id WHERE f.rel_path = 'generated.png'`).Scan(&d)
		if got := d.Format("2006-01-02"); got != "2025-01-02" {
			t.Errorf("generated.png re-dated to %s, want 2025-01-02 (its older copy was excluded)", got)
		}
		// A rescan leaves the excluded folders alone.
		before := count(`SELECT count(*) FROM files`)
		scans := count(`SELECT count(*) FROM river_job WHERE kind = 'scan_library' AND state = 'completed'`)
		res, err := rc.Insert(ctx, jobs.ScanLibraryArgs{LibraryID: lib.ID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.UniqueSkippedAsDuplicate {
			t.Fatal("a rescan was skipped as a duplicate of the finished scan")
		}
		waitForJobs(t, ctx, s)
		if n := count(`SELECT count(*) FROM river_job WHERE kind = 'scan_library' AND state = 'completed'`); n != scans+1 {
			t.Errorf("completed scans = %d, want %d", n, scans+1)
		}
		if after := count(`SELECT count(*) FROM files`); after != before {
			t.Errorf("rescan changed the file count from %d to %d", before, after)
		}
	})
}

func TestFirstRunSetup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	h, _ := api.Handler(api.Options{Store: s, ServerZone: "America/Los_Angeles"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	var st struct {
		Needed     bool   `json:"needed"`
		ServerZone string `json:"serverZone"`
	}
	getJSON(t, c, srv.URL+"/api/v1/setup", &st)
	if !st.Needed || st.ServerZone != "America/Los_Angeles" {
		t.Fatalf("setup status = %+v", st)
	}
	if code := status(t, c, "POST", srv.URL+"/api/v1/setup", `{"displayName":"Rich","email":"rich@example.test","password":"short","timeZone":"Europe/London"}`); code != 422 {
		t.Errorf("short password = %d, want 422", code)
	}
	if code := status(t, c, "POST", srv.URL+"/api/v1/setup", `{"displayName":"Rich","email":"rich@example.test","password":"a long enough password","timeZone":"Europe/London"}`); code != 200 {
		t.Fatalf("setup = %d", code)
	}
	var me struct {
		DisplayName    string `json:"displayName"`
		Role           string `json:"role"`
		TimeZone       string `json:"timeZone"`
		TimeZoneChosen bool   `json:"timeZoneChosen"`
	}
	getJSON(t, c, srv.URL+"/api/v1/auth/me", &me) // the setup response signed us in
	if me.Role != "admin" || me.TimeZone != "Europe/London" || !me.TimeZoneChosen || me.DisplayName != "Rich" {
		t.Errorf("me = %+v", me)
	}
	getJSON(t, c, srv.URL+"/api/v1/setup", &st)
	if st.Needed {
		t.Error("setup still needed after creating the first account")
	}
}

// tempDatabase creates a fresh database next to the development one and drops it afterwards.
func tempDatabase(t *testing.T, ctx context.Context) *store.Store {
	base := os.Getenv("PHOTOSTEAD_DATABASE_URL")
	if base == "" {
		t.Skip("PHOTOSTEAD_DATABASE_URL not set (run via make test)")
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Skipf("Postgres not reachable: %v", err)
	}
	name := fmt.Sprintf("photostead_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	s, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	})
	return s
}

func waitForJobs(t *testing.T, ctx context.Context, s *store.Store) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var open int
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE state NOT IN ('completed', 'discarded', 'cancelled')`).Scan(&open)
		var total int
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&total)
		if open == 0 && total > 1 {
			var failed []string
			rows, _ := s.Pool.Query(ctx, `SELECT kind || ': ' || coalesce(errors::text, '') FROM river_job WHERE state = 'discarded'`)
			for rows.Next() {
				var e string
				_ = rows.Scan(&e)
				failed = append(failed, e)
			}
			if len(failed) > 0 {
				t.Fatalf("jobs failed: %v", failed)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("jobs did not finish in time")
}

func copyTree(t *testing.T, src, dst string) {
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, _ := d.Info()
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return err
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fixtureTime is the modification time the fixture scripts give their files: 03:04:05 on
// 2025-01-02 in Los Angeles, the same day in UTC and Tokyo.
var fixtureTime = time.Date(2025, 1, 2, 11, 4, 5, 0, time.UTC)

// setModTimes sets the modification times of files in a copied fixture tree. A fresh checkout
// dates every file today, so tests that depend on file dates set them here.
func setModTimes(t *testing.T, root string, times map[string]time.Time) {
	for rel, mt := range times {
		if err := os.Chtimes(filepath.Join(root, rel), mt, mt); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshot lists every path with its size and modification time.
func snapshot(t *testing.T, root string) string {
	var b strings.Builder
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, _ := d.Info()
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(&b, "%s %d %d|", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return b.String()
}

func makeReadOnly(t *testing.T, root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if d != nil && !d.IsDir() {
			return os.Chmod(p, 0o444)
		}
		return nil
	})
	// Directories last, deepest first, so the walk itself isn't blocked.
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Chmod(dirs[i], 0o555)
	}
}

func makeWritable(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if d != nil {
			_ = os.Chmod(p, map[bool]os.FileMode{true: 0o755, false: 0o644}[d.IsDir()])
		}
		return nil
	})
}

func do(t *testing.T, c *http.Client, method, u, body string, headers map[string]string) *http.Response {
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func status(t *testing.T, c *http.Client, method, u, body string) int {
	return do(t, c, method, u, body, nil).StatusCode
}

func getJSON(t *testing.T, c *http.Client, u string, v any) {
	resp := do(t, c, "GET", u, "", nil)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = %d %s", u, resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

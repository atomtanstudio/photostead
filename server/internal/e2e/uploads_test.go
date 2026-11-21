package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/store/db"
)

// Uploads from other programs: a file is filed by day in the managed library, indexed, and given
// what came with it; the same bytes again add only their description.
func TestUploads(t *testing.T) {
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
	share := t.TempDir()
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photostead", Kind: "managed", RootPath: share, ReadOnly: false, SidecarMode: "off"}); err != nil {
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
	render := media.Renderer{DataDir: t.TempDir()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rc, err := river.NewClient(riverpgxv5.New(s.Pool), &river.Config{
		Logger: log, FetchPollInterval: 100 * time.Millisecond,
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
	h, _ := api.Handler(api.Options{Store: s, Render: render, Jobs: rc, ServerZone: "UTC"})
	srv := httptest.NewServer(h)
	defer srv.Close()

	key := api.NewAPIKey()
	if _, err := s.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: owner.ID, Name: "ComfyUI sync", TokenHash: api.APIKeyHash(key)}); err != nil {
		t.Fatal(err)
	}
	upload := func(token, name string, data []byte, meta map[string]any) (int, map[string]any) {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		if meta != nil {
			mp, _ := mw.CreateFormField("meta")
			_ = json.NewEncoder(mp).Encode(meta)
		}
		fp, _ := mw.CreateFormFile("file", name)
		_, _ = fp.Write(data)
		_ = mw.Close()
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/uploads", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	png := pngWith(t, 7, nil)
	sum := sha256.Sum256(png)

	if code, _ := upload("", "a.png", png, nil); code != 401 {
		t.Errorf("no key = %d, want 401", code)
	}
	if code, _ := upload("psk_wrong", "a.png", png, nil); code != 401 {
		t.Errorf("a wrong key = %d, want 401", code)
	}
	if code, _ := upload(key, "notes.txt", []byte("hello"), nil); code != 415 {
		t.Errorf("a text file = %d, want 415", code)
	}

	taken := time.Date(2026, 10, 12, 21, 30, 0, 0, time.UTC)
	code, out := upload(key, "../ComfyUI_00042_.png", png, map[string]any{"takenAt": taken, "description": "A lighthouse at dusk",
		"sourceUrl": "https://example.test/run/42", "keywords": []string{"comfyui", "lighthouse"}, "album": "ComfyUI"})
	if code != 202 || out["status"] != "accepted" || out["path"] != "photos/2026/2026-10-12/ComfyUI_00042_.png" || out["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("upload = %d %v", code, out)
	}
	// An upload makes a single job (waitForJobs expects a scan as well): wait for the item instead.
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		var n int
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE sha256 = $1 AND render_state <> 'pending'`, sum[:]).Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the upload wasn't indexed")
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(share, ".uploads")); len(entries) != 0 {
		t.Errorf("staging not cleaned up: %d files", len(entries))
	}
	var assetID string
	_ = s.Pool.QueryRow(ctx, `SELECT id::text FROM assets WHERE sha256 = $1`, sum[:]).Scan(&assetID)

	jar, _ := cookiejar.New(nil)
	c := srv.Client()
	c.Jar = jar
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	var detail api.Asset
	getJSON(t, c, srv.URL+"/api/v1/assets/"+assetID, &detail)
	if detail.Description != "A lighthouse at dusk\n\nhttps://example.test/run/42" || !slices.Equal(detail.Keywords, []string{"comfyui", "lighthouse"}) ||
		len(detail.Albums) != 1 || detail.Albums[0].Name != "ComfyUI" || detail.LocalDate != "2026-10-12" {
		t.Errorf("uploaded item = %q %v %v %s", detail.Description, detail.Keywords, detail.Albums, detail.LocalDate)
	}

	// The same bytes again (the same picture saved from another post): no second copy, both texts.
	code, out = upload(key, "x-1-1.png", png, map[string]any{"description": "Reposted", "album": "ComfyUI"})
	if code != 200 || out["status"] != "duplicate" || out["assetId"] != assetID {
		t.Fatalf("second upload = %d %v", code, out)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+assetID, &detail)
	if !strings.HasSuffix(detail.Description, "\n\nReposted") || len(detail.Albums) != 1 {
		t.Errorf("after the duplicate: %q %v", detail.Description, detail.Albums)
	}
	var files int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM files WHERE state = 'present'`).Scan(&files)
	if files != 1 {
		t.Errorf("%d files present, want 1", files)
	}

	// Programs can ask what's already here, with the key.
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/uploads/check", strings.NewReader(`{"sha256":["`+hex.EncodeToString(sum[:])+`","`+strings.Repeat("0", 64)+`"],"album":"Seen by the sync"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var known struct{ Known []string }
	_ = json.NewDecoder(res.Body).Decode(&known)
	res.Body.Close()
	if res.StatusCode != 200 || !slices.Equal(known.Known, []string{hex.EncodeToString(sum[:])}) {
		t.Errorf("check = %d %v", res.StatusCode, known.Known)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+assetID, &detail)
	if len(detail.Albums) != 2 {
		t.Errorf("checking with an album should put the known item in it: %v", detail.Albums)
	}

	// From an importer: a favorite, archived, four stars, and where it was taken (the file doesn't
	// say), applied once it's indexed.
	imported := pngWith(t, 21, nil)
	isum := sha256.Sum256(imported)
	code, out = upload(key, "IMG_2019.png", imported, map[string]any{"takenAt": "2019-07-04T18:30:00Z", "favorite": true, "archived": true,
		"rating": 4, "latitude": 48.8584, "longitude": 2.2945})
	if code != 202 {
		t.Fatalf("imported upload = %d %v", code, out)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		var n int
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM uploads WHERE sha256 = $1 AND applied_at IS NOT NULL`, isum[:]).Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the imported upload wasn't applied")
		}
	}
	var importedID string
	var fav bool
	var stars int
	_ = s.Pool.QueryRow(ctx, `SELECT a.id::text, coalesce(u.favorite, false), coalesce(u.rating, 0) FROM assets a
		LEFT JOIN asset_user_state u ON u.asset_id = a.id AND u.user_id = a.owner_id WHERE a.sha256 = $1`, isum[:]).Scan(&importedID, &fav, &stars)
	getJSON(t, c, srv.URL+"/api/v1/assets/"+importedID, &detail)
	if !fav || stars != 4 || !detail.Archived || detail.Location == nil || detail.LocalDate != "2019-07-04" {
		t.Errorf("imported item: favorite %v, %d stars, archived %v, location %v, %s", fav, stars, detail.Archived, detail.Location, detail.LocalDate)
	}

	// From an importer that knows what an AI item was asked for (a ChatGPT export): the prompt shows
	// when the file proves it's AI but doesn't say, never over the file's own, and not on a photo.
	applied := func(sum [32]byte, n int) string {
		t.Helper()
		for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
			var got int
			var id string
			_ = s.Pool.QueryRow(ctx, `SELECT count(*), coalesce(max(asset_id::text), '') FROM uploads WHERE sha256 = $1 AND applied_at IS NOT NULL`, sum[:]).Scan(&got, &id)
			if got == n {
				return id
			}
			if time.Now().After(deadline) {
				t.Fatalf("upload %d of %x wasn't applied", n, sum[:4])
			}
		}
	}
	item := func(id string) api.Asset {
		t.Helper()
		var a api.Asset
		getJSON(t, c, srv.URL+"/api/v1/assets/"+id, &a)
		return a
	}
	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description rdf:about="" ` +
		`xmlns:Iptc4xmpExt="http://iptc.org/std/Iptc4xmpExt/2008-02-29/" ` +
		`Iptc4xmpExt:DigitalSourceType="http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"/></rdf:RDF></x:xmpmeta>`
	made := pngWith(t, 31, map[string]string{"XML:com.adobe.xmp": xmp})
	msum := sha256.Sum256(made)
	if code, out := upload(key, "Teal vase.png", made, map[string]any{"prompt": "  A teal vase in soft natural light  "}); code != 202 {
		t.Fatalf("AI upload = %d %v", code, out)
	}
	madeID := applied(msum, 1)
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		detail = item(madeID)
		if detail.Generation != nil || time.Now().After(deadline) {
			break
		}
	}
	if detail.Generation == nil || detail.Generation.Prompt != "A teal vase in soft natural light" {
		t.Fatalf("an AI item's given prompt = %+v", detail.Generation)
	}
	// Brought again with a better prompt, and a description that only repeats it: the new prompt, once.
	if code, out := upload(key, "Teal vase.png", made, map[string]any{"prompt": "A teal vase on a sunlit sill", "description": "a teal  vase on a sunlit sill"}); code != 200 {
		t.Fatalf("AI upload again = %d %v", code, out)
	}
	detail = item(madeID)
	if detail.Generation == nil || detail.Generation.Prompt != "A teal vase on a sunlit sill" || detail.Description != "" {
		t.Errorf("after a second prompt: %+v, description %q", detail.Generation, detail.Description)
	}
	// A copy cut short, with its line breaks spelled out (as some apps keep them), isn't shown again
	// either; one written here always is.
	cut := pngWith(t, 32, map[string]string{"XML:com.adobe.xmp": xmp})
	if code, out := upload(key, "Vase 2.png", cut, map[string]any{"prompt": "A teal vase\non a sunlit sill, morning", "description": `A teal vase\non a sunlit`}); code != 202 {
		t.Fatalf("second AI upload = %d %v", code, out)
	}
	cutID := applied(sha256.Sum256(cut), 1)
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		if detail = item(cutID); detail.Generation != nil || time.Now().After(deadline) {
			break
		}
	}
	if detail.Generation == nil || detail.Description != "" {
		t.Errorf("a cut copy of the prompt: %+v, description %q", detail.Generation, detail.Description)
	}
	if code := status(t, c, "POST", srv.URL+"/api/v1/assets/description", `{"assetIds":["`+madeID+`"],"text":"A teal vase on a sunlit sill"}`); code != 200 {
		t.Fatalf("describe = %d", code)
	}
	if detail = item(madeID); detail.Description != "A teal vase on a sunlit sill" {
		t.Errorf("a description written here was hidden: %q", detail.Description)
	}

	// Read again by a newer parser: the given prompt is kept.
	if _, err := s.Pool.Exec(ctx, `UPDATE assets SET provenance_version = 0 WHERE id = $1`, madeID); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Insert(ctx, jobs.ProvenanceArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		var v int
		_ = s.Pool.QueryRow(ctx, `SELECT coalesce(provenance_version, 0) FROM assets WHERE id = $1`, madeID).Scan(&v)
		if v > 0 || time.Now().After(deadline) {
			break
		}
	}
	detail = item(madeID)
	if detail.Generation == nil || detail.Generation.Prompt != "A teal vase on a sunlit sill" {
		t.Errorf("after reading it again: %+v", detail.Generation)
	}

	a1111 := pngWith(t, 41, map[string]string{"parameters": "a lighthouse at dusk\nSteps: 25, Sampler: Euler a, CFG scale: 7, Seed: 99, Size: 64x48"})
	asum := sha256.Sum256(a1111)
	if code, out := upload(key, "00001.png", a1111, map[string]any{"prompt": "not what the file says"}); code != 202 {
		t.Fatalf("A1111 upload = %d %v", code, out)
	}
	ownID := applied(asum, 1)
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		detail = item(ownID)
		if detail.Generation != nil || time.Now().After(deadline) {
			break
		}
	}
	if detail.Generation == nil || detail.Generation.Prompt != "a lighthouse at dusk" {
		t.Errorf("the file's own prompt should win: %+v", detail.Generation)
	}
	if code, out := upload(key, "00001.png", a1111, map[string]any{"prompt": "still not what the file says"}); code != 200 {
		t.Fatalf("A1111 upload again = %d %v", code, out)
	}
	detail = item(ownID)
	if detail.Generation == nil || detail.Generation.Prompt != "a lighthouse at dusk" {
		t.Errorf("a duplicate's prompt replaced the file's own: %+v", detail.Generation)
	}

	photo := pngWith(t, 51, nil)
	psum := sha256.Sum256(photo)
	if code, out := upload(key, "IMG_0001.png", photo, map[string]any{"prompt": "a photo isn't prompted"}); code != 202 {
		t.Fatalf("photo upload = %d %v", code, out)
	}
	photoID := applied(psum, 1)
	time.Sleep(500 * time.Millisecond) // provenance is saved just after the upload is applied
	detail = item(photoID)
	if detail.Generation != nil || detail.AI != "" {
		t.Errorf("a photo with a given prompt became AI: %+v", detail.Generation)
	}

	// A revoked key stops working.
	if n, _ := s.Q.RevokeAPIKey(ctx, db.RevokeAPIKeyParams{UserID: owner.ID, Name: "ComfyUI sync"}); n != 1 {
		t.Fatal("revoke")
	}
	if code, _ := upload(key, "b.png", pngWith(t, 8, nil), nil); code != 401 {
		t.Errorf("a revoked key = %d", code)
	}
}

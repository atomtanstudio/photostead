package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/ask"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/labels"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/worker"
)

// unit is a 768-number vector at angle theta between axis 0 and axis k.
func unit(theta float64, k int) []float32 {
	v := make([]float32, 768)
	v[0] = float32(math.Cos(theta))
	v[k] = float32(math.Sin(theta))
	return v
}

func packed(v []float32) string {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestWorkersAndSearch(t *testing.T) {
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
	if _, err := rc.Insert(ctx, jobs.ScanLibraryArgs{LibraryID: lib.ID}, nil); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)
	if _, err := jobs.Pair(ctx, s.Q); err != nil {
		t.Fatal(err)
	}
	queued, err := s.Q.QueueEmbeddings(ctx, search.Model)
	if err != nil || queued < 8 {
		t.Fatalf("queued %d tasks (%v)", queued, err)
	}

	// A fake text service: every query embeds to axis 0.
	textUp := true
	text := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !textUp {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"model":"` + search.Model + `"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": search.Model, "embeddings": [][]float32{unit(0, 1)}})
	}))
	defer text.Close()
	h, _ := api.Handler(api.Options{Store: s, Render: render, Embedder: &search.Embedder{URL: text.URL}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	token := worker.NewToken()
	if _, err := s.Q.CreateWorker(ctx, db.CreateWorkerParams{Name: "test", TokenHash: api.WorkerTokenHash(token)}); err != nil {
		t.Fatal(err)
	}
	other := worker.NewToken()
	if _, err := s.Q.CreateWorker(ctx, db.CreateWorkerParams{Name: "other", TokenHash: api.WorkerTokenHash(other)}); err != nil {
		t.Fatal(err)
	}
	call := func(tok, method, path, body string, out any) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if out != nil {
			_ = json.NewDecoder(res.Body).Decode(out)
		} else {
			_, _ = io.Copy(io.Discard, res.Body)
		}
		return res.StatusCode
	}
	if code := call("nope", "POST", "/api/worker/v1/claim", `{"kinds":["embed_image"],"models":["`+search.Model+`"],"max":2}`, nil); code != 401 {
		t.Errorf("a bad token = %d, want 401", code)
	}
	if code := call(token, "POST", "/api/worker/v1/hello", `{"name":"test","version":"0.1.0","capabilities":{"gpu":false}}`, nil); code != 200 {
		t.Errorf("hello = %d", code)
	}
	var claim struct{ Tasks []api.Task }
	if code := call(token, "POST", "/api/worker/v1/claim", `{"kinds":["embed_image"],"models":["`+search.Model+`"],"max":100}`, &claim); code != 200 || len(claim.Tasks) != int(queued) {
		t.Fatalf("claim = %d, %d tasks (want %d)", code, len(claim.Tasks), queued)
	}
	// The input of a held task, but not to another worker.
	first := claim.Tasks[0]
	req, _ := http.NewRequest("GET", srv.URL+"/api/worker/v1/tasks/"+itoa(first.ID)+"/input", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/webp" || len(img) < 100 {
		t.Errorf("input = %d %s %d bytes", res.StatusCode, res.Header.Get("Content-Type"), len(img))
	}
	if code := call(other, "GET", "/api/worker/v1/tasks/"+itoa(first.ID)+"/input", "", nil); code != 404 {
		t.Errorf("another worker's input = %d, want 404", code)
	}
	// Results: item i at angle 0.1*i from axis 0 (so item 0 is the best match for axis 0), one
	// malformed vector, and one failure.
	theta := map[string]float64{}
	var results []map[string]any
	for i, task := range claim.Tasks {
		switch i {
		case len(claim.Tasks) - 1:
			results = append(results, map[string]any{"id": task.ID, "error": "couldn't decode"})
		case len(claim.Tasks) - 2:
			results = append(results, map[string]any{"id": task.ID, "embedding": packed(make([]float32, 768))}) // not a unit vector
		default:
			theta[task.AssetID.String()] = 0.1 * float64(i)
			results = append(results, map[string]any{"id": task.ID, "embedding": packed(unit(0.1*float64(i), 2+i))})
		}
	}
	b, _ := json.Marshal(map[string]any{"results": results})
	var saved struct{ Saved, Failed int }
	if code := call(token, "POST", "/api/worker/v1/results", string(b), &saved); code != 200 || saved.Saved != len(claim.Tasks)-2 || saved.Failed != 2 {
		t.Errorf("results = %d %+v", code, saved)
	}
	p, _ := s.Q.EmbeddingProgress(ctx, search.Model)
	if int(p.Embedded) != len(claim.Tasks)-2 || p.Waiting != 2 {
		t.Errorf("progress = %+v, want the two failures queued again", p)
	}

	// Search by meaning, through the API.
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	var found struct {
		Mode, Order string
		Assets      []api.TimelineAsset
		Meaning     api.MeaningStatus
	}
	getJSON(t, c, srv.URL+"/api/v1/search?q=a+red+fox", &found)
	if found.Mode != "meaning" || found.Order != "relevance" || !found.Meaning.Available || len(found.Assets) == 0 {
		t.Fatalf("search = %+v", found)
	}
	for i := 1; i < len(found.Assets); i++ {
		if theta[found.Assets[i-1].ID.String()] > theta[found.Assets[i].ID.String()] {
			t.Errorf("results out of order at %d", i)
		}
	}
	// Hybrid: a word that names two files lifts them to the top of a search by meaning.
	var hybrid struct {
		Mode   string
		Assets []api.TimelineAsset
		Found  *api.Found
	}
	getJSON(t, c, srv.URL+"/api/v1/search?q=beach", &hybrid)
	if hybrid.Mode != "meaning" || hybrid.Found == nil || hybrid.Found.Names != 2 {
		t.Fatalf("hybrid = %+v", hybrid)
	}
	var beaches []string
	_ = s.Pool.QueryRow(ctx, `SELECT array_agg(asset_id::text) FROM files WHERE rel_path ILIKE '%beach%' AND asset_id IS NOT NULL`).Scan(&beaches)
	top := map[string]bool{hybrid.Assets[0].ID.String(): true, hybrid.Assets[1].ID.String(): true, hybrid.Assets[2].ID.String(): true}
	for _, b := range beaches {
		if theta[b] != 0 && !top[b] { // the best look-alike (angle 0) may hold first place
			t.Errorf("a beach file isn't near the top: %v", hybrid.Assets[:3])
		}
	}
	best := found.Assets[0].ID.String()
	if theta[best] != 0 {
		t.Errorf("best match has angle %v, want 0", theta[best])
	}
	getJSON(t, c, srv.URL+"/api/v1/search?similar="+best, &found)
	if found.Mode != "similar" || len(found.Assets) == 0 || found.Assets[0].ID.String() == best {
		t.Errorf("similar = %+v", found)
	}
	for _, a := range found.Assets {
		if a.ID.String() == best {
			t.Error("an item is listed as like itself")
		}
	}
	// Text in pictures: OCR tasks get the larger preview, and the words they read are searchable.
	if n, err := s.Q.QueueOCR(ctx, search.OCREngine); err != nil || n != queued {
		t.Fatalf("queue OCR = %d %v, want %d", n, err, queued)
	}
	var reads struct{ Tasks []api.Task }
	if code := call(token, "POST", "/api/worker/v1/claim", `{"kinds":["ocr"],"models":["`+search.OCREngine+`"],"max":100}`, &reads); code != 200 || len(reads.Tasks) != int(queued) {
		t.Fatalf("claim OCR = %d, %d tasks", code, len(reads.Tasks))
	}
	req, _ = http.NewRequest("GET", srv.URL+"/api/worker/v1/tasks/"+itoa(reads.Tasks[0].ID)+"/input", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if res, err = http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	}
	preview, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if _, want := render.Paths(reads.Tasks[0].AssetID); res.StatusCode != 200 || !sameFile(t, want, preview) {
		t.Errorf("OCR input = %d, %d bytes: not the preview", res.StatusCode, len(preview))
	}
	sign := reads.Tasks[1].AssetID
	var texts []map[string]any
	for i, task := range reads.Tasks {
		switch i {
		case 1:
			texts = append(texts, map[string]any{"id": task.ID, "text": "NO PARKING\nZONE B\x00"})
		default:
			texts = append(texts, map[string]any{"id": task.ID, "text": ""}) // read, nothing found
		}
	}
	b, _ = json.Marshal(map[string]any{"results": texts})
	if code := call(token, "POST", "/api/worker/v1/results", string(b), &saved); code != 200 || saved.Saved != len(reads.Tasks) {
		t.Errorf("OCR results = %d %+v", code, saved)
	}
	if op, _ := s.Q.OCRProgress(ctx, search.OCREngine); int(op.Done) != len(reads.Tasks) || op.WithText != 1 || op.Waiting != 0 {
		t.Errorf("OCR progress = %+v", op)
	}
	if n, _ := s.Q.QueueOCR(ctx, search.OCREngine); n != 0 {
		t.Errorf("queued %d items again after reading them", n)
	}
	getJSON(t, c, srv.URL+"/api/v1/search?q=parking", &hybrid)
	if hybrid.Found == nil || hybrid.Found.Text != 1 || hybrid.Assets[0].ID != sign {
		t.Errorf("search for text in a picture = %+v", hybrid)
	}
	var detail api.Asset
	getJSON(t, c, srv.URL+"/api/v1/assets/"+sign.String(), &detail)
	if detail.Text != "NO PARKING\nZONE B" {
		t.Errorf("asset text = %q", detail.Text)
	}

	// Utility collections. Every prompt embeds to axis 0 here, so whatever looks like axis 0 matches
	// the first prompt (a screenshot); the sign also has a file name that says nothing.
	st, err := labels.Refresh(ctx, s, &search.Embedder{URL: text.URL})
	if err != nil || st.Checked != len(claim.Tasks)-2 || st.Labeled == 0 {
		t.Fatalf("labels = %+v %v", st, err)
	}
	var counts api.Counts
	getJSON(t, c, srv.URL+"/api/v1/library/counts", &counts)
	shots := counts.Labels["screenshot"]
	if shots != int32(st.Labeled) {
		t.Fatalf("counts = %+v, want %d screenshots", counts.Labels, st.Labeled)
	}
	var coll, all, hidden struct{ Total int64 }
	getJSON(t, c, srv.URL+"/api/v1/timeline/buckets?collection=screenshot", &coll)
	getJSON(t, c, srv.URL+"/api/v1/timeline/buckets", &all)
	getJSON(t, c, srv.URL+"/api/v1/timeline/buckets?hideUtility=true", &hidden)
	var inTimeline int64 // the timeline shows only the top of a stack; a collection shows every member
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM asset_labels l WHERE NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
		WHERE sa.asset_id = l.asset_id AND st.state = 'confirmed' AND st.cover_asset_id <> l.asset_id)`).Scan(&inTimeline)
	if coll.Total != int64(shots) || all.Total-hidden.Total != inTimeline || inTimeline == 0 {
		t.Errorf("screenshots %d, timeline %d, without utility %d (want %d fewer)", coll.Total, all.Total, hidden.Total, inTimeline)
	}
	// "Not a screenshot" sticks, even when everything is sorted again.
	var shot string
	_ = s.Pool.QueryRow(ctx, `SELECT asset_id::text FROM asset_labels WHERE label = 'screenshot' LIMIT 1`).Scan(&shot)
	if code := status(t, c, "POST", srv.URL+"/api/v1/labels/screenshot/remove", `{"assetIds":["`+shot+`"]}`); code != 200 {
		t.Errorf("remove label = %d", code)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM labels_checked`); err != nil {
		t.Fatal(err)
	}
	if _, err := labels.Refresh(ctx, s, &search.Embedder{URL: text.URL}); err != nil {
		t.Fatal(err)
	}
	getJSON(t, c, srv.URL+"/api/v1/timeline/buckets?collection=screenshot", &coll)
	getJSON(t, c, srv.URL+"/api/v1/assets/"+shot, &detail)
	if coll.Total != int64(shots)-1 || len(detail.Labels) != 0 {
		t.Errorf("after removing one: %d screenshots, its labels %v", coll.Total, detail.Labels)
	}
	if code := status(t, c, "POST", srv.URL+"/api/v1/labels/receipt/add", `{"assetIds":["`+shot+`"]}`); code != 200 {
		t.Errorf("add label = %d", code)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+shot, &detail)
	if !reflect.DeepEqual(detail.Labels, []string{"receipt"}) {
		t.Errorf("labels after adding = %v", detail.Labels)
	}

	// Ask: a question becomes filters; an empty answer offers the nearest one that isn't.
	type answer struct {
		Plan   ask.Plan
		Answer string
		Assets []api.TimelineAsset
		Total  int
		Exact  bool
		Alt    *ask.Alt
		Chips  []ask.Chip
	}
	askQ := func(body string) answer {
		t.Helper()
		var a answer
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/ask", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("ask %s = %d %s", body, res.StatusCode, b)
		}
		_ = json.NewDecoder(res.Body).Decode(&a)
		return a
	}
	a := askQ(`{"q":"photos that say parking"}`)
	if a.Total != 1 || !a.Exact || len(a.Assets) != 1 || a.Assets[0].ID != sign || a.Plan.OCR != "parking" {
		t.Errorf("ask parking = %+v", a)
	}
	none := askQ(`{"q":"videos that say parking"}`)
	if none.Total != 0 || none.Alt == nil || none.Alt.Remove != "type" || none.Alt.Count != 1 {
		t.Errorf("ask videos parking = %+v", none)
	}
	dropped := askQ(`{"q":"","prev":` + mustJSON(none.Plan) + `,"exclude":["type"]}`)
	if dropped.Total != 1 {
		t.Errorf("after dropping the type: %+v", dropped)
	}
	var images int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE type = 'image' AND motion_of IS NULL AND visibility = 'timeline'`).Scan(&images)
	many := askQ(`{"q":"how many photos"}`)
	if many.Total != images || !strings.HasPrefix(many.Answer, "You have ") {
		t.Errorf("how many photos = %d %q, want %d", many.Total, many.Answer, images)
	}
	looks := askQ(`{"q":"photos of a red fox"}`) // filters, then ranked by likeness
	if looks.Exact || looks.Total == 0 || looks.Plan.Text != "red fox" {
		t.Errorf("looks like = %+v", looks)
	}

	// With the text service down, search falls back to names.
	textUp = false
	h2, _ := api.Handler(api.Options{Store: s, Render: render, Embedder: &search.Embedder{URL: text.URL}})
	srv2 := httptest.NewServer(h2)
	defer srv2.Close()
	jar2, _ := cookiejar.New(nil)
	c2 := &http.Client{Jar: jar2}
	status(t, c2, "POST", srv2.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`)
	getJSON(t, c2, srv2.URL+"/api/v1/search?q=beach", &found)
	if found.Mode != "names" || found.Meaning.Available || len(found.Assets) != 2 {
		t.Errorf("fallback = %+v", found)
	}
	// A revoked worker is shut out.
	if n, _ := s.Q.RevokeWorker(ctx, "test"); n != 1 {
		t.Fatal("revoke")
	}
	if code := call(token, "POST", "/api/worker/v1/claim", `{"kinds":["embed_image"],"models":["`+search.Model+`"],"max":2}`, nil); code != 401 {
		t.Errorf("a revoked worker = %d, want 401", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// sameFile says whether got is the file at path.
func sameFile(t *testing.T, path string, got []byte) bool {
	t.Helper()
	want, err := os.ReadFile(path)
	return err == nil && bytes.Equal(want, got)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

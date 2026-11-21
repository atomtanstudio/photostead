package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/labels"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/worker"
)

// TestAdultScans: a worker's probabilities for every picture of an item become its label (a video
// needs two frames to agree), a worker that sends only one picture's for a video is turned away, and
// a new rule relabels old scans from their kept probabilities, leaving the owner's corrections.
func TestAdultScans(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := auth.Setup(ctx, s, "Rich", "rich@example.test", "a long enough password", "Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	item := func(kind string, n int) uuid.UUID {
		var id uuid.UUID
		sum := sha256.Sum256(fmt.Appendf(nil, "%s %d", kind, n))
		if err := s.Pool.QueryRow(ctx, `INSERT INTO assets (owner_id, type, sha256, taken_at, local_date, date_source, date_basis, render_state, duration_ms)
			VALUES ($1, $2, $3, now(), current_date, 'file', 'instant', 'done', CASE WHEN $2 = 'video' THEN 60000 END) RETURNING id`,
			u.ID, kind, sum[:]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	lingerie, nude, closeUp, musicVideo, film, badVideo := item("image", 1), item("image", 2), item("image", 3), item("video", 4), item("video", 5), item("video", 6)

	h, _ := api.Handler(api.Options{Store: s, Render: media.Renderer{DataDir: t.TempDir()}})
	srv := httptest.NewServer(h)
	defer srv.Close()
	token := worker.NewToken()
	if _, err := s.Q.CreateWorker(ctx, db.CreateWorkerParams{Name: "test", TokenHash: api.WorkerTokenHash(token)}); err != nil {
		t.Fatal(err)
	}
	call := func(path, body string, out any) int {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if out != nil {
			_ = json.NewDecoder(res.Body).Decode(out)
		}
		return res.StatusCode
	}
	if n, err := s.Q.QueueAdult(ctx, labels.AdultEngine); err != nil || n != 6 {
		t.Fatalf("queued %d (%v)", n, err)
	}
	var claim struct{ Tasks []api.Task }
	if code := call("/api/worker/v1/claim", `{"kinds":["adult"],"models":["`+labels.AdultEngine+`"],"max":10}`, &claim); code != 200 || len(claim.Tasks) != 6 {
		t.Fatalf("claim = %d, %d tasks", code, len(claim.Tasks))
	}
	none, sexy, naked, skin := "0.9,0.1,0,0", "0.1,0.4,0.5,0", "0,0,0.3,0.7", "0.4,0,0.06,0.54"
	scores := map[uuid.UUID]struct {
		pictures []string
		frames   int
	}{
		lingerie:   {[]string{"0,0.02,0.98,0"}, 1},
		nude:       {[]string{naked}, 1},
		closeUp:    {[]string{skin}, 1},
		musicVideo: {[]string{none, sexy, none, naked, none, sexy, none}, 7}, // one odd frame, two in costume
		film:       {[]string{none, naked, naked, none, none, none, none}, 7},
		badVideo:   {[]string{naked}, 7}, // an old worker: its most adult frame only
	}
	var results []string
	for _, task := range claim.Tasks {
		if task.Kind != "adult" || (task.AssetID == musicVideo && task.Frames != 6) {
			t.Errorf("task %+v: want an adult task, 6 frames for a video", task)
		}
		sc := scores[task.AssetID]
		results = append(results, fmt.Sprintf(`{"id":%d,"adult":{"scores":[%s],"frames":%d}}`, task.ID, strings.Join(sc.pictures, ","), sc.frames))
	}
	var saved struct{ Saved int }
	if code := call("/api/worker/v1/results", `{"results":[`+strings.Join(results, ",")+`]}`, &saved); code != 200 || saved.Saved != 5 {
		t.Fatalf("results = %d, saved %d (want 5: the old worker's video turned away)", code, saved.Saved)
	}
	label := func(id uuid.UUID) string {
		var l string
		_ = s.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(label || ':' || basis, ',' ORDER BY label), '') FROM asset_labels WHERE asset_id = $1`, id).Scan(&l)
		return l
	}
	for id, want := range map[uuid.UUID]string{lingerie: "suggestive:visual", nude: "adult:visual", closeUp: "",
		musicVideo: "suggestive:visual", film: "adult:visual", badVideo: ""} {
		if got := label(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
	var frames, rule int
	if err := s.Pool.QueryRow(ctx, `SELECT frames, rule FROM adult_scans WHERE asset_id = $1`, film).Scan(&frames, &rule); err != nil || frames != 7 || rule != labels.AdultRule {
		t.Errorf("the film's scan: %d frames, rule %d (%v)", frames, rule, err)
	}

	// Scans labelled by the first rule, which called lingerie adult: one left as it was, one the
	// owner said isn't adult. Relabelled, the first is suggestive; the owner's word stays.
	if _, err := s.Pool.Exec(ctx, `UPDATE adult_scans SET rule = 1 WHERE asset_id = ANY($1)`, []uuid.UUID{lingerie, nude}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM asset_labels WHERE asset_id = $1;`, lingerie); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO asset_labels (asset_id, label, basis, score) VALUES ($1, 'adult', 'visual', 0.98), ($2, 'adult', 'rejected', NULL)
		ON CONFLICT (asset_id, label) DO UPDATE SET basis = EXCLUDED.basis`, lingerie, nude); err != nil {
		t.Fatal(err)
	}
	if n, err := jobs.RelabelAdult(ctx, s); err != nil || n != 2 {
		t.Fatalf("relabelled %d (%v)", n, err)
	}
	if got := label(lingerie); got != "suggestive:visual" {
		t.Errorf("lingerie relabelled: %q", got)
	}
	if got := label(nude); got != "adult:rejected" {
		t.Errorf("the owner's correction: %q", got)
	}
	if n, err := jobs.RelabelAdult(ctx, s); err != nil || n != 0 {
		t.Errorf("a second run relabelled %d (%v)", n, err)
	}
}

// "Adult content: hidden" keeps adult items out of a character's sample pictures too, as it does
// from the timeline and places (the iPhone's Characters page and the web's show these samples).
func TestAdultHiddenFromSamples(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
	h, _ := api.Handler(api.Options{Store: s, ServerZone: "UTC"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	var character uuid.UUID
	if err := s.Pool.QueryRow(ctx, `INSERT INTO characters (owner_id, name) VALUES ($1, 'Nova') RETURNING id`, owner.ID).Scan(&character); err != nil {
		t.Fatal(err)
	}
	item := func(name string, adult bool) uuid.UUID {
		var id uuid.UUID
		sum := sha256.Sum256([]byte(name))
		if err := s.Pool.QueryRow(ctx, `INSERT INTO assets (owner_id, type, sha256, taken_at, local_date, date_source, date_basis, render_state)
			VALUES ($1, 'image', $2, now(), current_date, 'file', 'instant', 'done') RETURNING id`, owner.ID, sum[:]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO character_assets (character_id, asset_id, basis) VALUES ($1, $2, 'confirmed')`, character, id); err != nil {
			t.Fatal(err)
		}
		if adult {
			if _, err := s.Pool.Exec(ctx, `INSERT INTO asset_labels (asset_id, label, basis, score) VALUES ($1, 'adult', 'visual', 0.97)`, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	plain, explicit := item("plain", false), item("explicit", true)
	samples := func() []uuid.UUID {
		t.Helper()
		var out struct{ Characters []api.Character }
		getJSON(t, c, srv.URL+"/api/v1/characters", &out)
		var ids []uuid.UUID
		for _, ch := range out.Characters {
			for _, sm := range ch.Samples {
				ids = append(ids, sm.ID)
			}
		}
		return ids
	}
	if got := samples(); len(got) != 2 || !slices.Contains(got, explicit) {
		t.Errorf("shown: samples %v, want both", got)
	}
	if code := doJSON(t, c, "PATCH", srv.URL+"/api/v1/auth/me/settings", `{"adult":"hide"}`, nil); code != 200 {
		t.Fatalf("hiding adult content = %d", code)
	}
	if got := samples(); len(got) != 1 || got[0] != plain {
		t.Errorf("hidden: samples %v, want only %v", got, plain)
	}
}

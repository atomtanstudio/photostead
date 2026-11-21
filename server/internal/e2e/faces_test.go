package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
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

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/faces"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/metadata"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/worker"
)

// face is a 512-number unit vector close to axis k (a person), nudged by i so no two are alike.
func face(k, i int) []float32 {
	v := make([]float32, 512)
	v[k] = 1
	v[100+i] = 0.15
	n := float32(math.Sqrt(1 + 0.15*0.15))
	for j := range v {
		v[j] /= n
	}
	return v
}

// Faces from workers to people: found faces are saved, grouped into people (named from Immich's
// suggestions when they agree), and corrected by the owner, all through the API.
func TestFacesAndPeople(t *testing.T) {
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
	h, _ := api.Handler(api.Options{Store: s, Render: render})
	srv := httptest.NewServer(h)
	defer srv.Close()
	token := worker.NewToken()
	if _, err := s.Q.CreateWorker(ctx, db.CreateWorkerParams{Name: "test", TokenHash: api.WorkerTokenHash(token)}); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, out any) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
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

	queued, err := s.Q.QueueFaces(ctx, faces.Engine)
	if err != nil || queued < 6 {
		t.Fatalf("queued %d faces tasks (%v)", queued, err)
	}
	var claim struct{ Tasks []api.Task }
	if code := call("POST", "/api/worker/v1/claim", `{"kinds":["faces"],"models":["`+faces.Engine+`"],"max":100}`, &claim); code != 200 || len(claim.Tasks) != int(queued) {
		t.Fatalf("claim = %d, %d tasks (want %d)", code, len(claim.Tasks), queued)
	}
	// Workers read the preview, so a face's box is a fraction of it.
	req, _ := http.NewRequest("GET", srv.URL+"/api/worker/v1/tasks/"+itoa(claim.Tasks[0].ID)+"/input", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if _, want := render.Paths(claim.Tasks[0].AssetID); res.StatusCode != 200 || !sameFile(t, want, input) {
		t.Errorf("faces input = %d, %d bytes: not the preview", res.StatusCode, len(input))
	}

	// Which items are real photos shown in the timeline (people are made from those), and which are AI.
	shown := map[uuid.UUID]bool{}
	rows, _ := s.Pool.Query(ctx, `SELECT id FROM visible_assets($1) WHERE visibility = 'timeline' AND motion_of IS NULL AND source_type NOT IN ('ai_generated', 'ai_edited')`, owner.ID)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		shown[id] = true
	}
	rows.Close()
	var real, ai []api.Task
	for _, task := range claim.Tasks {
		var src string
		_ = s.Pool.QueryRow(ctx, `SELECT source_type FROM assets WHERE id = $1`, task.AssetID).Scan(&src)
		switch {
		case src == "ai_generated" || src == "ai_edited":
			ai = append(ai, task)
		case shown[task.AssetID]:
			real = append(real, task)
		}
	}
	if len(ai) == 0 && len(real) > 7 { // made by a model, as the evidence in its file would say
		ai, real = real[len(real)-1:], real[:len(real)-1]
		if _, err := s.Pool.Exec(ctx, `UPDATE assets SET source_type = 'ai_generated' WHERE id = $1`, ai[0].AssetID); err != nil {
			t.Fatal(err)
		}
	}
	if len(real) < 7 || len(ai) < 1 {
		t.Fatalf("%d real items and %d AI ones: the test needs 7 and 1", len(real), len(ai))
	}
	r := func(i int) uuid.UUID { return real[i].AssetID }

	// Immich named Alice in items 0-3, in an AI image (where an import can over-merge), in item 5 (where
	// no face turns up) and in item 6, whose result is malformed (so it isn't looked at yet).
	var alice uuid.UUID
	if err := s.Pool.QueryRow(ctx, `INSERT INTO people (owner_id, name) VALUES ($1, 'Alice') RETURNING id`, owner.ID).Scan(&alice); err != nil {
		t.Fatal(err)
	}
	for _, a := range []uuid.UUID{r(0), r(1), r(2), r(3), ai[0].AssetID, r(5)} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO person_suggestions (person_id, asset_id, source) VALUES ($1, $2, 'immich')`, alice, a); err != nil {
			t.Fatal(err)
		}
	}
	failed := r(6)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO person_suggestions (person_id, asset_id, source) VALUES ($1, $2, 'immich')`, alice, failed); err != nil {
		t.Fatal(err)
	}

	// Alice (axis 1) in items 0-3; someone else (axis 2) in items 1, 2 and 4; a stranger (axis 3)
	// once, in item 4; three Alice-like faces in the AI image; none in item 5.
	good := func(k, i int) map[string]any {
		return map[string]any{"box": []float32{0.3, 0.2, 0.2, 0.3}, "landmarks": []float32{0.35, 0.3, 0.45, 0.3, 0.4, 0.35, 0.36, 0.42, 0.44, 0.42},
			"score": 0.95, "px": 120, "yaw": 0.02, "blur": 150, "embedding": packed(face(k, i))}
	}
	found := map[uuid.UUID][]map[string]any{
		r(0): {good(1, 0)}, r(1): {good(1, 1), good(2, 2)}, r(2): {good(1, 3), good(2, 4)}, r(3): {good(1, 5)},
		r(4): {good(2, 6), good(3, 7)}, r(5): {}, ai[0].AssetID: {good(1, 8), good(1, 9), good(1, 10)},
	}
	var results []map[string]any
	for _, task := range claim.Tasks {
		switch fs, ok := found[task.AssetID]; {
		case task.AssetID == failed: // a malformed face: the task fails and nothing of it is kept
			bad := good(1, 11)
			bad["embedding"] = packed(make([]float32, 512))
			results = append(results, map[string]any{"id": task.ID, "faces": []any{bad}})
		case ok:
			results = append(results, map[string]any{"id": task.ID, "faces": fs})
		default:
			results = append(results, map[string]any{"id": task.ID, "faces": []any{}})
		}
	}
	var saved struct{ Saved, Failed int }
	if code := call("POST", "/api/worker/v1/results", mustJSON(map[string]any{"results": results}), &saved); code != 200 || saved.Failed != 1 || saved.Saved != len(results)-1 {
		t.Fatalf("results = %d %+v", code, saved)
	}
	p, _ := s.Q.FaceProgress(ctx, faces.Engine)
	if int(p.Scanned) != len(results)-1 || p.Faces != 11 || p.Waiting != 1 {
		t.Errorf("progress = %+v, want %d scanned, 11 faces, the failure queued again", p, len(results)-1)
	}
	if n, _ := s.Q.QueueFaces(ctx, faces.Engine); n != 0 {
		t.Errorf("queued %d items again after scanning them", n)
	}

	// Grouping: Alice's faces become Immich's Alice; the other three, someone new and unnamed; the
	// stranger waits; the AI faces are never grouped.
	if due, _ := s.Q.FaceRunDue(ctx, owner.ID); !due {
		t.Fatal("grouping isn't due with new faces")
	}
	st, err := faces.Run(ctx, s, owner.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Faces != 8 || st.People != 2 || st.New != 2 || st.Named != 1 || st.Waiting != 1 {
		t.Errorf("grouping = %+v", st)
	}
	if due, _ := s.Q.FaceRunDue(ctx, owner.ID); due {
		t.Error("grouping is still due right after a run (it would run every time)")
	}

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	var list struct{ People []api.Person }
	getJSON(t, c, srv.URL+"/api/v1/people", &list)
	if len(list.People) != 2 || list.People[0].ID != alice || list.People[1].Name != "" {
		t.Fatalf("people = %+v", list.People)
	}
	// Alice: the four items with her face, and the one not looked at yet (Immich's word stands
	// there). Immich's suggestions for the AI image and for item 5 no longer count.
	a, other := list.People[0], list.People[1]
	const wantCount = 5
	if a.Count != wantCount || a.FaceItems != 4 || a.CoverFace == nil || a.Origin != "immich" {
		t.Errorf("Alice = %+v, want %d items, 4 by face", a, wantCount)
	}
	if other.Count != 3 || other.FaceItems != 3 || other.Origin != "faces" || other.CoverFace == nil {
		t.Errorf("the unnamed person = %+v", other)
	}
	resp := do(t, c, "GET", srv.URL+"/api/v1/faces/"+a.CoverFace.String()+"/thumbnail", "", nil)
	if thumb, _ := io.ReadAll(resp.Body); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/webp" || len(thumb) < 100 {
		t.Errorf("face thumbnail = %d %s, %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(thumb))
	}
	if code := status(t, c, "GET", srv.URL+"/api/v1/faces/"+uuid.NewString()+"/thumbnail", ""); code != 404 {
		t.Errorf("an unknown face = %d", code)
	}
	var detail api.Asset
	getJSON(t, c, srv.URL+"/api/v1/assets/"+r(1).String(), &detail)
	if len(detail.People) != 2 || detail.People[0].ID != alice || detail.People[0].Source != "faces" {
		t.Errorf("item 1's people = %+v", detail.People)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+r(5).String(), &detail)
	if len(detail.People) != 0 {
		t.Errorf("item 5 (no face found) still lists %+v", detail.People)
	}
	var buckets struct{ Total int }
	getJSON(t, c, srv.URL+"/api/v1/timeline/buckets?collection=person:"+alice.String(), &buckets)
	if buckets.Total != wantCount {
		t.Errorf("Alice's timeline = %d items, want %d", buckets.Total, wantCount)
	}

	// Naming the new person makes them the owner's; hiding keeps them out of People.
	var bob api.Person
	if code := doJSON(t, c, "PATCH", srv.URL+"/api/v1/people/"+other.ID.String(), `{"name":" Bob "}`, &bob); code != 200 || bob.Name != "Bob" || bob.Origin != "user" {
		t.Errorf("rename = %d %+v", code, bob)
	}
	if code := doJSON(t, c, "PATCH", srv.URL+"/api/v1/people/"+other.ID.String(), `{"hidden":true}`, &bob); code != 200 || !bob.Hidden || bob.Name != "Bob" {
		t.Errorf("hide = %d %+v", code, bob)
	}
	getJSON(t, c, srv.URL+"/api/v1/people", &list)
	if len(list.People) != 1 {
		t.Errorf("a hidden person is listed: %+v", list.People)
	}
	getJSON(t, c, srv.URL+"/api/v1/people?hidden=true", &list)
	if len(list.People) != 1 || list.People[0].ID != other.ID {
		t.Errorf("hidden people = %+v", list.People)
	}
	_ = doJSON(t, c, "PATCH", srv.URL+"/api/v1/people/"+other.ID.String(), `{"hidden":false}`, &bob)

	// "Not Bob" in item 4: his face there comes out, and grouping doesn't put it back.
	var changed struct{ Changed int64 }
	if code := doJSON(t, c, "POST", srv.URL+"/api/v1/people/"+other.ID.String()+"/remove", mustJSON(map[string]any{"assetIds": []uuid.UUID{r(4)}}), &changed); code != 200 || changed.Changed != 1 {
		t.Errorf("remove = %d %+v", code, changed)
	}
	if due, _ := s.Q.FaceRunDue(ctx, owner.ID); !due {
		t.Error("grouping isn't due after a correction")
	}
	if st, err = faces.Run(ctx, s, owner.ID, nil); err != nil || st.Waiting != 2 || st.Placed != 0 {
		t.Errorf("grouping after the correction = %+v %v", st, err)
	}
	getJSON(t, c, srv.URL+"/api/v1/people/"+other.ID.String(), &bob)
	if bob.Count != 2 {
		t.Errorf("Bob = %+v, want 2 items", bob)
	}

	// Bob is Alice after all: one person, with every item of both, and item 4 still kept out.
	var merged api.Person
	if code := doJSON(t, c, "POST", srv.URL+"/api/v1/people/"+other.ID.String()+"/merge", `{"into":"`+alice.String()+`"}`, &merged); code != 200 || merged.ID != alice || merged.Count != wantCount {
		t.Errorf("merge = %d %+v", code, merged)
	}
	if code := status(t, c, "GET", srv.URL+"/api/v1/people/"+other.ID.String(), ""); code != 404 {
		t.Errorf("Bob after the merge = %d", code)
	}
	if _, err = faces.Run(ctx, s, owner.ID, nil); err != nil {
		t.Fatal(err)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+r(4).String(), &detail)
	if len(detail.People) != 0 {
		t.Errorf("item 4 went back to Alice: %+v", detail.People)
	}

	// Alice is an AI character after all (her images lost their metadata): a new character named
	// Nova gets the items with her face, and she leaves People.
	var nova api.Character
	if code := doJSON(t, c, "POST", srv.URL+"/api/v1/people/"+alice.String()+"/character", `{"name":" Nova "}`, &nova); code != 200 || nova.Name != "Nova" || nova.Count != 4 || nova.FaceGroups != 1 {
		t.Fatalf("to a character = %d %+v", code, nova)
	}
	getJSON(t, c, srv.URL+"/api/v1/people", &list)
	if len(list.People) != 0 {
		t.Errorf("a character is still in People: %+v", list.People)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+r(0).String(), &detail)
	if len(detail.People) != 0 || len(detail.Characters) != 1 || detail.Characters[0].ID != nova.ID || detail.Characters[0].Source != "faces" {
		t.Errorf("item 0 = people %+v, characters %+v", detail.People, detail.Characters)
	}
	// Naming another group after her finds the same character, not a second one.
	var again api.Character
	if code := doJSON(t, c, "POST", srv.URL+"/api/v1/people/"+alice.String()+"/character", `{"name":"nova"}`, &again); code != 200 || again.ID != nova.ID {
		t.Errorf("by an existing name = %d %+v", code, again)
	}
	// "Not this character" for item 0 holds through grouping; the rest stay hers.
	if code := doJSON(t, c, "POST", srv.URL+"/api/v1/characters/"+nova.ID.String()+"/remove", mustJSON(map[string]any{"assetIds": []uuid.UUID{r(0)}}), &nova); code != 200 || nova.Count != 3 {
		t.Errorf("remove from the character = %d %+v", code, nova)
	}
	if _, err = faces.Run(ctx, s, owner.ID, nil); err != nil {
		t.Fatal(err)
	}
	getJSON(t, c, srv.URL+"/api/v1/characters/"+nova.ID.String(), &nova)
	if nova.Count != 3 {
		t.Errorf("after grouping, Nova = %+v", nova)
	}
	// Her LoRAs can be left empty while she has faces.
	if code := doJSON(t, c, "PUT", srv.URL+"/api/v1/characters/"+nova.ID.String(), `{"name":"Nova","patterns":[]}`, &nova); code != 200 {
		t.Errorf("edit without LoRAs = %d", code)
	}
	// Back to People: the faces' items leave the character, Alice is listed again.
	var changed2 struct{ Changed int64 }
	if code := doJSON(t, c, "DELETE", srv.URL+"/api/v1/characters/"+nova.ID.String()+"/faces", "", &changed2); code != 200 || changed2.Changed != 1 {
		t.Errorf("faces back to People = %d %+v", code, changed2)
	}
	getJSON(t, c, srv.URL+"/api/v1/characters/"+nova.ID.String(), &nova)
	getJSON(t, c, srv.URL+"/api/v1/people", &list)
	if nova.Count != 0 || nova.FaceGroups != 0 || len(list.People) != 1 || list.People[0].ID != alice {
		t.Errorf("after giving the faces back: Nova %+v, people %+v", nova, list.People)
	}

	// Pets: real photos are looked at for cats and dogs (AI images aren't), and the animals are
	// grouped per species apart from people. A cat in items 0 and 1 is a suggested pet; a dog seen
	// once waits, and so do two look-alike cats in one photo (two animals, not one pet). A weak
	// detection near the cat is left out.
	queuedPets, err := s.Q.QueuePets(ctx, faces.PetEngine)
	if err != nil || queuedPets < 6 {
		t.Fatalf("queued %d pets tasks (%v)", queuedPets, err)
	}
	var petClaim struct{ Tasks []api.Task }
	if code := call("POST", "/api/worker/v1/claim", `{"kinds":["pets"],"models":["`+faces.PetEngine+`"],"max":100}`, &petClaim); code != 200 || len(petClaim.Tasks) != int(queuedPets) {
		t.Fatalf("claim pets = %d, %d tasks (want %d)", code, len(petClaim.Tasks), queuedPets)
	}
	animal := func(species string, k, i int) map[string]any {
		return map[string]any{"species": species, "box": []float32{0.2, 0.3, 0.5, 0.4}, "score": 0.9, "px": 300, "embedding": packed(face(k, i))}
	}
	weak := animal("cat", 10, 25)
	weak["score"] = 0.4
	pets := map[uuid.UUID][]map[string]any{r(0): {animal("cat", 10, 20)}, r(1): {animal("cat", 10, 21)}, r(2): {animal("dog", 11, 22)},
		r(3): {animal("cat", 12, 23), animal("cat", 12, 24)}, r(4): {weak}}
	var petResults []map[string]any
	for _, task := range petClaim.Tasks {
		if task.AssetID == ai[0].AssetID {
			t.Error("an AI image was queued for pets")
		}
		found := pets[task.AssetID]
		if found == nil {
			found = []map[string]any{}
		}
		petResults = append(petResults, map[string]any{"id": task.ID, "pets": found})
	}
	var before, after int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM faces WHERE species IS NULL`).Scan(&before)
	if code := call("POST", "/api/worker/v1/results", mustJSON(map[string]any{"results": petResults}), &saved); code != 200 || saved.Saved != len(petResults) {
		t.Fatalf("pet results = %d %+v", code, saved)
	}
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM faces WHERE species IS NULL`).Scan(&after)
	if before != after {
		t.Errorf("saving pets changed people's faces: %d -> %d", before, after)
	}
	if pp, _ := s.Q.PetProgress(ctx, faces.PetEngine); int(pp.Scanned) != len(petResults) || pp.Pets != 6 || pp.Waiting != 0 {
		t.Errorf("pet progress = %+v", pp)
	}
	if st, err = faces.Run(ctx, s, owner.ID, nil); err != nil || st.Pets != 5 || st.PetGroups != 1 || st.PetWaiting != 3 {
		t.Errorf("grouping with pets = %+v %v", st, err)
	}
	getJSON(t, c, srv.URL+"/api/v1/people", &list)
	var cat *api.Person
	for i := range list.People {
		if list.People[i].Kind == "pet" {
			cat = &list.People[i]
		}
	}
	if len(list.People) != 2 || cat == nil || cat.Species != "cat" || cat.Count != 2 || cat.CoverFace == nil {
		t.Fatalf("people with a pet = %+v", list.People)
	}
	resp = do(t, c, "GET", srv.URL+"/api/v1/faces/"+cat.CoverFace.String()+"/thumbnail", "", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/webp" {
		t.Errorf("pet thumbnail = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+r(0).String(), &detail)
	kinds := map[string]bool{}
	for _, p := range detail.People {
		kinds[p.Kind] = true
	}
	if len(detail.People) != 2 || !kinds["human"] || !kinds["pet"] {
		t.Errorf("item 0's people and pets = %+v", detail.People)
	}
	if code := doJSON(t, c, "POST", srv.URL+"/api/v1/people/"+cat.ID.String()+"/merge", `{"into":"`+alice.String()+`"}`, nil); code != 422 {
		t.Errorf("merging a cat into a person = %d, want 422", code)
	}
	var miso api.Person
	if code := doJSON(t, c, "PATCH", srv.URL+"/api/v1/people/"+cat.ID.String(), `{"name":"Miso"}`, &miso); code != 200 || miso.Name != "Miso" || miso.Kind != "pet" {
		t.Errorf("naming the cat = %d %+v", code, miso)
	}
}

// doJSON sends body and decodes the reply into out, returning the status.
func doJSON(t *testing.T, c *http.Client, method, u, body string, out any) int {
	t.Helper()
	resp := do(t, c, method, u, body, nil)
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

package e2e

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// pngWith makes a small PNG carrying tEXt chunks, the way ComfyUI and Automatic1111 save them.
func pngWith(t *testing.T, seed uint8, text map[string]string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := range img.Pix {
		img.Pix[i] = seed + uint8(i%7)
	}
	img.Set(0, 0, color.RGBA{seed, 1, 2, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	var chunks bytes.Buffer
	for k, v := range text {
		data := append([]byte(k+"\x00"), v...)
		_ = binary.Write(&chunks, binary.BigEndian, uint32(len(data)))
		body := append([]byte("tEXt"), data...)
		chunks.Write(body)
		_ = binary.Write(&chunks, binary.BigEndian, crc32.ChecksumIEEE(body))
	}
	const afterIHDR = 8 + 25
	return append(append(append([]byte{}, raw[:afterIHDR]...), chunks.Bytes()...), raw[afterIHDR:]...)
}

func TestProvenance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	libDir := t.TempDir()

	// A ComfyUI graph bigger than the index keeps (8 KB), with a long note node to make it so.
	graph := `{
	 "1": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "flux1-dev.safetensors"}},
	 "2": {"class_type": "LoraLoader", "inputs": {"model": ["1", 0], "clip": ["1", 1], "lora_name": "Ada.safetensors", "strength_model": 0.9}},
	 "3": {"class_type": "CLIPTextEncode", "inputs": {"clip": ["2", 1], "text": "ada reading in a sunlit library"}},
	 "4": {"class_type": "CLIPTextEncode", "inputs": {"clip": ["2", 1], "text": "blurry"}},
	 "5": {"class_type": "KSampler", "inputs": {"model": ["2", 0], "positive": ["3", 0], "negative": ["4", 0], "seed": 18446744073709551615,
	       "steps": 20, "cfg": 3.5, "sampler_name": "euler", "scheduler": "simple", "denoise": 1}},
	 "6": {"class_type": "Note", "inputs": {"note": "` + strings.Repeat("padding ", 1500) + `"}}
	}`
	files := map[string][]byte{
		"comfy.png": pngWith(t, 10, map[string]string{"prompt": graph, "workflow": `{"nodes": [{"id": 5, "type": "KSampler"}], "links": []}`}),
		"a1111.png": pngWith(t, 40, map[string]string{"parameters": "a lighthouse at dusk\nNegative prompt: people\nSteps: 25, Sampler: Euler a, CFG scale: 7, Seed: 99, Size: 64x48, Model: dreamshaper_8"}),
		"upscale.png": pngWith(t, 70, map[string]string{"prompt": `{"1": {"class_type": "LoadImage", "inputs": {"image": "x.png"}},
			"2": {"class_type": "ImageScaleBy", "inputs": {"image": ["1", 0], "scale_by": 2}}, "3": {"class_type": "SaveImage", "inputs": {"images": ["2", 0]}}}`}),
		"plain.png": pngWith(t, 100, nil),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(libDir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An image-to-video clip made from comfy.png, with its graph in the comment the way
	// VideoHelperSuite writes it.
	clipGraph := `{"1": {"class_type": "LoadImage", "inputs": {"image": "comfy.png"}},
	  "2": {"class_type": "UNETLoader", "inputs": {"unet_name": "wan2.2_i2v_14B.safetensors"}},
	  "3": {"class_type": "WanImageToVideo", "inputs": {"start_image": ["1", 0], "positive": ["4", 0]}},
	  "4": {"class_type": "CLIPTextEncode", "inputs": {"text": "ada turns to the window"}},
	  "5": {"class_type": "KSampler", "inputs": {"model": ["2", 0], "positive": ["3", 0], "seed": 7, "steps": 4, "cfg": 1}}}`
	comment, _ := json.Marshal(map[string]string{"prompt": clipGraph})
	clip := filepath.Join(libDir, "clip.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=gray:s=64x48:d=1", "-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-metadata", "comment="+string(comment), clip).CombinedOutput(); err != nil {
		t.Fatalf("making the clip: %v %s", err, out)
	}
	// Give the clip a later date than the image it came from.
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(clip, later, later)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
	lib, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "ai", Kind: "indexed", RootPath: libDir, ReadOnly: true, SidecarMode: "shadow"})
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
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}, jobs.QueueImages: {MaxWorkers: 2},
			jobs.QueueVideos: {MaxWorkers: 1}, jobs.QueueProvenance: {MaxWorkers: 1}},
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

	type row struct {
		source, tool, model, prompt, sourceType string
		graphLen                                int
		needsFile, assetNeedsFile               bool
	}
	get := func(name string) row {
		t.Helper()
		var r row
		err := s.Pool.QueryRow(ctx, `
			SELECT coalesce(g.source, ''), coalesce(g.tool, ''), coalesce(m.display, ''), coalesce(p.text, ''), a.source_type,
			       coalesce(length(g.graph), 0), coalesce(g.needs_file, false), a.provenance_needs_file
			FROM files f JOIN assets a ON a.id = f.asset_id
			LEFT JOIN generations g ON g.asset_id = a.id LEFT JOIN ai_models m ON m.id = g.model_id LEFT JOIN prompts p ON p.id = g.prompt_id
			WHERE f.rel_path = $1`, name).Scan(&r.source, &r.tool, &r.model, &r.prompt, &r.sourceType, &r.graphLen, &r.needsFile, &r.assetNeedsFile)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return r
	}
	check := func(stage string, wantComfyNeedsFile bool) {
		t.Helper()
		c := get("comfy.png")
		if c.source != "comfyui" || c.sourceType != "ai_generated" || c.needsFile != wantComfyNeedsFile || c.assetNeedsFile != wantComfyNeedsFile {
			t.Errorf("%s: comfy.png = %+v", stage, c)
		}
		if !wantComfyNeedsFile && (c.prompt != "ada reading in a sunlit library" || c.model != "flux1-dev" || c.graphLen < 8<<10) {
			t.Errorf("%s: comfy.png details = %+v", stage, c)
		}
		if a := get("a1111.png"); a.source != "a1111" || a.model != "dreamshaper_8" || a.prompt != "a lighthouse at dusk" || a.sourceType != "ai_generated" {
			t.Errorf("%s: a1111.png = %+v", stage, a)
		}
		for _, name := range []string{"upscale.png", "plain.png"} {
			if r := get(name); r.source != "" || r.sourceType != "unknown" {
				t.Errorf("%s: %s = %+v, want no generation", stage, name, r)
			}
		}
	}
	// Ingest reads the whole of the metadata: everything complete straight away.
	check("after ingest", false)

	// The pass over the index (as for items indexed before provenance existed) can only see the
	// cut copy: ComfyUI's, details to come from the file.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM generations; UPDATE assets SET provenance_version = NULL, source_type = 'unknown'`); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Insert(ctx, jobs.ProvenanceArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)
	check("after the index pass", true)

	// Reading the files completes it.
	if _, err := rc.Insert(ctx, jobs.ProvenanceFilesArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)
	check("after reading the files", false)

	// The API: the badge on the timeline, the details, the workflow download.
	h, _ := api.Handler(api.Options{Store: s, Render: render})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := status(t, c, "POST", srv.URL+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	var buckets struct{ Buckets []struct{ Month string } }
	getJSON(t, c, srv.URL+"/api/v1/timeline/buckets", &buckets)
	ai := map[string]string{}
	for _, b := range buckets.Buckets {
		var m struct {
			Assets []struct{ ID, AI string }
		}
		getJSON(t, c, srv.URL+"/api/v1/timeline?month="+b.Month, &m)
		for _, a := range m.Assets {
			ai[a.ID] = a.AI
		}
	}
	var comfyID string
	_ = s.Pool.QueryRow(ctx, `SELECT asset_id::text FROM files WHERE rel_path = 'comfy.png'`).Scan(&comfyID)
	if len(ai) != 5 || ai[comfyID] != "generated" {
		t.Errorf("timeline AI labels = %v", ai)
	}
	labelled := 0
	for _, v := range ai {
		if v != "" {
			labelled++
		}
	}
	if labelled != 3 {
		t.Errorf("%d items labelled AI, want 3 (two images and the clip)", labelled)
	}
	var detail api.Asset
	getJSON(t, c, srv.URL+"/api/v1/assets/"+comfyID, &detail)
	g := detail.Generation
	if g == nil || g.Tool != "ComfyUI" || g.Model != "flux1-dev" || g.Prompt != "ada reading in a sunlit library" || g.Negative != "blurry" ||
		g.Seed != "18446744073709551615" || g.Steps != 20 || g.CFG != 3.5 || !g.HasWorkflow || g.Incomplete || len(g.Loras) != 1 ||
		g.Loras[0].Name != "Ada" || *g.Loras[0].Weight != 0.9 || g.PromptUses != 1 {
		t.Errorf("generation = %+v", g)
	}
	resp := do(t, c, "GET", srv.URL+"/api/v1/assets/"+comfyID+"/workflow", "", nil)
	body, _ := io.ReadAll(resp.Body)
	var wf map[string]any
	if resp.StatusCode != 200 || json.Unmarshal(body, &wf) != nil || wf["nodes"] == nil ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "comfy-workflow.json") {
		t.Errorf("workflow download = %d %s %.80s", resp.StatusCode, resp.Header.Get("Content-Disposition"), body)
	}
	// The prompt library and the model & LoRA catalog.
	var prompts struct {
		Total   int
		Prompts []api.Prompt
	}
	getJSON(t, c, srv.URL+"/api/v1/prompts", &prompts)
	if prompts.Total != 3 || len(prompts.Prompts) != 3 || len(prompts.Prompts[0].Samples) != 1 {
		t.Fatalf("prompts = %+v", prompts)
	}
	byText := map[string]api.Prompt{}
	for _, p := range prompts.Prompts {
		byText[p.Text] = p
	}
	ada := byText["ada reading in a sunlit library"]
	if ada.Uses != 1 || len(ada.Models) != 1 || ada.Models[0] != "flux1-dev" || len(ada.Loras) != 1 || ada.Loras[0] != "Ada" {
		t.Errorf("ada's prompt = %+v", ada)
	}
	getJSON(t, c, srv.URL+"/api/v1/prompts?q=LIGHTHOUSE", &prompts)
	if prompts.Total != 1 || prompts.Prompts[0].Text != "a lighthouse at dusk" {
		t.Errorf("search = %+v", prompts)
	}
	getJSON(t, c, srv.URL+"/api/v1/prompts?q=%25", &prompts) // a literal %, not a wildcard
	if prompts.Total != 0 {
		t.Errorf("a %% matched %d prompts", prompts.Total)
	}
	var models struct{ Models, Loras []api.AIModel }
	getJSON(t, c, srv.URL+"/api/v1/models", &models)
	if len(models.Models) != 3 || len(models.Loras) != 1 || models.Loras[0].Name != "Ada" || models.Loras[0].With != "flux1-dev" {
		t.Fatalf("models = %+v", models)
	}
	for _, m := range models.Models {
		if m.Name == "flux1-dev" && (m.With != "Ada" || m.Uses != 1 || len(m.Samples) != 1) {
			t.Errorf("flux1-dev = %+v", m)
		}
	}
	collectionItems := func(col string) int {
		var b struct{ Total int }
		getJSON(t, c, srv.URL+"/api/v1/timeline/buckets?collection="+col, &b)
		return b.Total
	}
	if n := collectionItems("prompt:" + ada.ID.String()); n != 1 {
		t.Errorf("prompt collection = %d items", n)
	}
	var one api.AIModel
	getJSON(t, c, srv.URL+"/api/v1/models/"+models.Loras[0].ID.String(), &one)
	if one.Name != "Ada" || one.With != "flux1-dev" || len(one.Samples) != 1 {
		t.Errorf("one LoRA = %+v", one)
	}
	if n := collectionItems("lora:" + models.Loras[0].ID.String()); n != 1 {
		t.Errorf("lora collection = %d items", n)
	}
	var counts api.Counts
	getJSON(t, c, srv.URL+"/api/v1/library/counts", &counts)
	if counts.Prompts != 3 || counts.Models != 4 {
		t.Errorf("counts = %+v", counts)
	}
	if g.PromptID == nil || *g.PromptID != ada.ID || g.ModelID == nil || g.Loras[0].ID != models.Loras[0].ID {
		t.Errorf("the generation's links = %+v", g)
	}

	// The clip shows the image it was made from, found by its unique name (there are no
	// embeddings to compare looks with here), with no character needed for it.
	var clipID string
	_ = s.Pool.QueryRow(ctx, `SELECT asset_id::text FROM files WHERE rel_path = 'clip.mp4'`).Scan(&clipID)
	clipFrom := func() *api.Sample {
		t.Helper()
		var d api.Asset
		getJSON(t, c, srv.URL+"/api/v1/assets/"+clipID, &d)
		if d.Generation == nil {
			t.Fatalf("the clip has no generation")
		}
		return d.Generation.From
	}
	if f := clipFrom(); f == nil || f.ID.String() != comfyID || len(f.ThumbHash) == 0 {
		t.Errorf("the clip made from = %+v, want comfy.png", f)
	}
	var byLook bool
	if err := s.Pool.QueryRow(ctx, `SELECT look IS NOT NULL FROM generation_starts WHERE video_id = $1`, clipID).Scan(&byLook); err != nil || byLook {
		t.Errorf("the clip's start: by look = %v, %v; want by name", byLook, err)
	}
	if g.From != nil {
		t.Errorf("comfy.png made from = %+v, want nothing", g.From)
	}
	// A start image the viewer can't see isn't shown.
	if _, err := s.Pool.Exec(ctx, `UPDATE assets SET trashed_at = now() WHERE id = $1`, comfyID); err != nil {
		t.Fatal(err)
	}
	if f := clipFrom(); f != nil {
		t.Errorf("with comfy.png in the trash, the clip made from = %+v", f)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE assets SET trashed_at = NULL WHERE id = $1`, comfyID); err != nil {
		t.Fatal(err)
	}

	// Characters: Ada from her LoRA, and the clip made from her image (lineage).
	var ada2 api.Character
	if code := send(c, srv.URL, "POST", "/api/v1/characters", `{"name": "Ada", "patterns": ["^ada$"]}`, &ada2); code != 201 ||
		ada2.Count != 2 || len(ada2.Loras) != 1 || ada2.Loras[0] != "Ada" || len(ada2.Samples) != 2 || ada2.Samples[0].ID.String() != comfyID {
		t.Fatalf("create = %d %+v", code, ada2)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/characters", `{"name": "Ada", "patterns": ["x"]}`, nil); code != 409 {
		t.Errorf("a second Ada = %d, want 409", code)
	}
	if code := send(c, srv.URL, "POST", "/api/v1/characters", `{"name": "Bad", "patterns": ["(unclosed"]}`, nil); code != 422 {
		t.Errorf("a bad pattern = %d, want 422", code)
	}
	if n := collectionItems("character:" + ada2.ID.String()); n != 2 {
		t.Errorf("Ada's collection = %d", n)
	}
	getJSON(t, c, srv.URL+"/api/v1/assets/"+clipID, &detail)
	if len(detail.Characters) != 1 || detail.Characters[0].Name != "Ada" || detail.Characters[0].Source != "lineage" {
		t.Errorf("the clip's characters = %+v", detail.Characters)
	}
	// Taking the clip out sticks, even after the matching runs again.
	if code := send(c, srv.URL, "POST", "/api/v1/characters/"+ada2.ID.String()+"/remove", `{"assetIds": ["`+clipID+`"]}`, &ada2); code != 200 || ada2.Count != 1 {
		t.Errorf("remove = %d %+v", code, ada2)
	}
	if _, err := rc.Insert(ctx, jobs.CharactersArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	waitForJobs(t, ctx, s)
	if n := collectionItems("character:" + ada2.ID.String()); n != 1 {
		t.Errorf("after a refresh, Ada's collection = %d, want the clip still out", n)
	}
	if code := send(c, srv.URL, "PUT", "/api/v1/characters/"+ada2.ID.String(), `{"name": "Ada Lovelace", "patterns": ["^ada$"]}`, &ada2); code != 200 || ada2.Name != "Ada Lovelace" || ada2.Count != 1 {
		t.Errorf("rename = %d %+v", code, ada2)
	}
	getJSON(t, c, srv.URL+"/api/v1/library/counts", &counts)
	if counts.Characters != 1 {
		t.Errorf("counts.characters = %d", counts.Characters)
	}
	if code := send(c, srv.URL, "DELETE", "/api/v1/characters/"+ada2.ID.String(), "", nil); code != 204 {
		t.Errorf("delete = %d", code)
	}

	var plainID string
	_ = s.Pool.QueryRow(ctx, `SELECT asset_id::text FROM files WHERE rel_path = 'plain.png'`).Scan(&plainID)
	if code := status(t, c, "GET", srv.URL+"/api/v1/assets/"+plainID+"/workflow", ""); code != 404 {
		t.Errorf("workflow of a plain photo = %d, want 404", code)
	}
}

// send makes a request with a JSON body and decodes the answer into out (when given).
func send(c *http.Client, base, method, path, body string, out any) int {
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/labels"
	"photostead.com/server/internal/media"
	"photostead.com/server/internal/store/db"
)

// The worker API (/api/worker/v1): workers pull tasks with a revocable token, fetch only the inputs
// of tasks they hold, and post results. Docs: architecture.md, "Jobs, workers and AI calls".

var workerAuth = []map[string][]string{{"worker": {}}}

type workerKey struct{}

func workerID(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(workerKey{}).(uuid.UUID)
	return id
}

// WorkerTokenHash is how a worker token is stored.
func WorkerTokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// requireWorker checks a worker's bearer token.
func (s *server) requireWorker(ctx huma.Context, next func(huma.Context)) {
	token, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer ")
	if !ok || token == "" {
		_ = huma.WriteErr(s.api, ctx, http.StatusUnauthorized, "A worker token is needed.")
		return
	}
	w, err := s.Store.Q.WorkerByToken(ctx.Context(), WorkerTokenHash(token))
	if err != nil {
		_ = huma.WriteErr(s.api, ctx, http.StatusUnauthorized, "That worker token isn't valid (or was revoked).")
		return
	}
	next(huma.WithValue(ctx, workerKey{}, w.ID))
}

type helloInput struct {
	Body struct {
		Name         string         `json:"name" maxLength:"100"`
		Version      string         `json:"version" maxLength:"40"`
		Capabilities map[string]any `json:"capabilities"`
	}
}

type helloOutput struct {
	Body struct {
		ID uuid.UUID `json:"id"`
	}
}

type claimInput struct {
	Body struct {
		Kinds  []string `json:"kinds" minItems:"1"`
		Models []string `json:"models" minItems:"1"`
		Max    int32    `json:"max" minimum:"1" maximum:"512"`
	}
}

type Task struct {
	ID      int64      `json:"id"`
	Kind    string     `json:"kind"`
	AssetID uuid.UUID  `json:"assetId"`
	Model   string     `json:"model"`
	Frames  int        `json:"frames,omitempty" doc:"adult, for a video: how many frames to look at besides its poster (input?frame=1…frames)"`
	Video   *VideoTask `json:"video,omitempty" doc:"transcode: the video to copy (read it from /tasks/{id}/original, send the copy to PUT /tasks/{id}/copy)"`
}

type taskInputQuery struct {
	ID    int64 `path:"id"`
	Frame int   `query:"frame" minimum:"0" maximum:"16" doc:"For a video's adult task: a frame (1…frames), spread evenly through it; 0 is its poster"`
}

// videoFrames is how many frames an adult task looks at in a video, besides its poster.
func videoFrames(durationMs int32) int {
	if durationMs < 3000 {
		return 2
	}
	return 6
}

type claimOutput struct {
	Body struct {
		Tasks        []Task `json:"tasks"`
		LeaseSeconds int    `json:"leaseSeconds"`
	}
}

type taskInput struct {
	ID int64 `path:"id"`
}

type resultsInput struct {
	Body struct {
		Results []struct {
			ID        int64         `json:"id"`
			Embedding string        `json:"embedding,omitempty" doc:"embed_image: little-endian float32, base64"`
			Text      *string       `json:"text,omitempty" maxLength:"8000" doc:"ocr: the text read, one line per line ('' when there's none)"`
			Faces     *[]FaceResult `json:"faces,omitempty" maxItems:"100" doc:"faces: every face found (none is an answer too)"`
			Pets      *[]PetResult  `json:"pets,omitempty" maxItems:"100" doc:"pets: every cat and dog found (none is an answer too)"`
			Adult     *AdultResult  `json:"adult,omitempty" doc:"adult: what the model saw"`
			Error     string        `json:"error,omitempty" maxLength:"1000"`
		} `json:"results" maxItems:"2048"`
	}
}

// FaceResult is one face a worker found.
type FaceResult struct {
	Box       []float32 `json:"box" minItems:"4" maxItems:"4" doc:"x, y, width, height, as fractions of the picture"`
	Landmarks []float32 `json:"landmarks" minItems:"10" maxItems:"10" doc:"eyes, nose tip, mouth corners: x, y pairs, as fractions"`
	Score     float32   `json:"score"`
	Px        float32   `json:"px" doc:"the face's smaller side, in pixels of the input"`
	Yaw       float32   `json:"yaw"`
	Blur      float32   `json:"blur"`
	Embedding string    `json:"embedding" doc:"512 little-endian float32, base64"`
}

// AdultResult is how an item rates for adult content: each picture looked at, in turn.
type AdultResult struct {
	Scores []float32 `json:"scores" minItems:"4" maxItems:"68" doc:"Each picture's probabilities of neutral, low, medium and high, four after four: a photo's one picture; a video's poster, then its frames"`
	Frames int       `json:"frames" minimum:"1" maximum:"17" doc:"How many pictures were looked at (1 for a photo)"`
}

// PetResult is one cat or dog a worker found.
type PetResult struct {
	Species   string    `json:"species" enum:"cat,dog"`
	Box       []float32 `json:"box" minItems:"4" maxItems:"4" doc:"the whole animal: x, y, width, height, as fractions of the picture"`
	Score     float32   `json:"score"`
	Px        float32   `json:"px" doc:"the box's smaller side, in pixels of the input"`
	Embedding string    `json:"embedding" doc:"512 little-endian float32, base64"`
}

type resultsOutput struct {
	Body struct {
		Saved  int64 `json:"saved"`
		Failed int   `json:"failed"`
	}
}

type releaseInput struct {
	Body struct {
		IDs []int64 `json:"ids" maxItems:"2048"`
	}
}

const leaseSeconds = 600

// vectorText turns a packed float32 embedding into pgvector's text form, refusing anything that
// isn't a unit-length vector of a known size.
func vectorText(b64 string, dims int) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw)%4 != 0 || len(raw)/4 != dims {
		return "", false
	}
	var b strings.Builder
	b.WriteByte('[')
	var norm float64
	for i := 0; i < len(raw); i += 4 {
		f := math.Float32frombits(binary.LittleEndian.Uint32(raw[i:]))
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return "", false
		}
		norm += float64(f) * float64(f)
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', 6, 32))
	}
	b.WriteByte(']')
	return b.String(), math.Abs(norm-1) < 0.01
}

func (s *server) registerWorkers() {
	huma.Register(s.api, huma.Operation{
		OperationID: "workerHello", Method: http.MethodPost, Path: "/api/worker/v1/hello", Tags: []string{"Workers"},
		Summary: "A worker says who it is and what it can do", Security: workerAuth,
	}, func(ctx context.Context, in *helloInput) (*helloOutput, error) {
		caps, _ := json.Marshal(in.Body.Capabilities)
		if err := s.Store.Q.WorkerHello(ctx, db.WorkerHelloParams{ID: workerID(ctx), Version: pgtype.Text{String: in.Body.Version, Valid: in.Body.Version != ""}, Capabilities: caps}); err != nil {
			return nil, err
		}
		out := &helloOutput{}
		out.Body.ID = workerID(ctx)
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "workerClaim", Method: http.MethodPost, Path: "/api/worker/v1/claim", Tags: []string{"Workers"},
		Summary: "Lease up to max tasks", Description: "A lease lasts ten minutes; tasks not reported by then go to another worker.",
		Security: workerAuth,
	}, func(ctx context.Context, in *claimInput) (*claimOutput, error) {
		rows, err := s.Store.Q.ClaimTasks(ctx, db.ClaimTasksParams{WorkerID: uuid.NullUUID{UUID: workerID(ctx), Valid: true},
			LeaseSeconds: leaseSeconds, Kinds: in.Body.Kinds, Models: in.Body.Models, MaxTasks: in.Body.Max})
		if err != nil {
			return nil, err
		}
		out := &claimOutput{}
		out.Body.LeaseSeconds = leaseSeconds
		out.Body.Tasks = make([]Task, len(rows))
		var adult, transcode []uuid.UUID
		for i, r := range rows {
			out.Body.Tasks[i] = Task{ID: r.ID, Kind: r.Kind, AssetID: r.AssetID, Model: r.Model}
			switch r.Kind {
			case "adult":
				adult = append(adult, r.AssetID)
			case "transcode":
				transcode = append(transcode, r.AssetID)
			}
		}
		if len(transcode) > 0 {
			vids, err := s.Store.Q.TranscodeInputs(ctx, transcode)
			if err != nil {
				return nil, err
			}
			spec := map[uuid.UUID]*VideoTask{}
			for _, v := range vids {
				spec[v.AssetID] = &VideoTask{Recipe: media.CopyRecipe, HDR: v.Hdr, Width: v.Width, Height: v.Height, DurationMs: v.DurationMs,
					KeepVideo: v.PicturePlays}
			}
			for i := range out.Body.Tasks {
				if t := &out.Body.Tasks[i]; t.Kind == "transcode" {
					t.Video = spec[t.AssetID]
				}
			}
		}
		if len(adult) > 0 { // a video is looked at in several places, not only its poster
			videos, err := s.Store.Q.TaskVideos(ctx, adult)
			if err != nil {
				return nil, err
			}
			frames := map[uuid.UUID]int{}
			for _, v := range videos {
				frames[v.ID] = videoFrames(v.DurationMs)
			}
			for i := range out.Body.Tasks {
				if t := &out.Body.Tasks[i]; t.Kind == "adult" {
					t.Frames = frames[t.AssetID]
				}
			}
		}
		_ = s.Store.Q.TouchWorker(ctx, workerID(ctx)) // seen now
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "workerInput", Method: http.MethodGet, Path: "/api/worker/v1/tasks/{id}/input", Tags: []string{"Workers"},
		Summary: "A held task's input (the item's thumbnail; for ocr, faces, pets and adult, its larger preview)", Security: workerAuth,
		Description: "For a video's adult task, frame=1…frames gives a frame from that far through it (a JPEG).",
		Responses: map[string]*huma.Response{"200": {Description: "WebP image (a video's frame: JPEG)",
			Content: map[string]*huma.MediaType{"image/webp": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
	}, func(ctx context.Context, in *taskInputQuery) (*huma.StreamResponse, error) {
		t, err := s.Store.Q.LeasedTask(ctx, db.LeasedTaskParams{ID: in.ID, WorkerID: uuid.NullUUID{UUID: workerID(ctx), Valid: true}})
		if err != nil {
			return nil, huma.Error404NotFound("This worker doesn't hold that task.")
		}
		if in.Frame > 0 {
			return s.taskFrame(ctx, t.AssetID, t.Kind, in.Frame)
		}
		file, preview := s.Render.Paths(t.AssetID)
		if t.Kind == "ocr" || t.Kind == "faces" || t.Kind == "pets" || t.Kind == "adult" { // text, faces, animals and bodies need the larger render
			if _, err := os.Stat(preview); err == nil {
				file = preview
			}
		}
		if _, err := os.Stat(file); err != nil {
			return nil, huma.Error404NotFound("Not rendered.")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			r, w := humago.Unwrap(hctx)
			w.Header().Set("Content-Type", "image/webp")
			http.ServeFile(w, r, file)
		}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "workerResults", Method: http.MethodPost, Path: "/api/worker/v1/results", Tags: []string{"Workers"},
		Summary: "Report finished (or failed) tasks", Security: workerAuth, MaxBodyBytes: 16 << 20,
	}, func(ctx context.Context, in *resultsInput) (*resultsOutput, error) {
		var ids, textIDs, failedIDs []int64
		var vecs, texts, errs []string
		var facesSaved int64
		for _, r := range in.Body.Results {
			if r.Error == "" && r.Faces != nil {
				if err := s.saveFaces(ctx, r.ID, *r.Faces); err != nil {
					failedIDs, errs = append(failedIDs, r.ID), append(errs, err.Error())
				} else {
					facesSaved++
				}
				continue
			}
			if r.Error == "" && r.Pets != nil {
				if err := s.savePets(ctx, r.ID, *r.Pets); err != nil {
					failedIDs, errs = append(failedIDs, r.ID), append(errs, err.Error())
				} else {
					facesSaved++
				}
				continue
			}
			if r.Error == "" && r.Adult != nil {
				if err := s.saveAdult(ctx, r.ID, *r.Adult); err != nil {
					failedIDs, errs = append(failedIDs, r.ID), append(errs, err.Error())
				} else {
					facesSaved++
				}
				continue
			}
			if r.Error == "" && r.Text != nil {
				textIDs, texts = append(textIDs, r.ID), append(texts, strings.TrimSpace(strings.ReplaceAll(*r.Text, "\x00", "")))
				continue
			}
			v, ok := "", false
			if r.Error == "" {
				v, ok = vectorText(r.Embedding, 768)
			}
			if ok {
				ids, vecs = append(ids, r.ID), append(vecs, v)
				continue
			}
			msg := r.Error
			if msg == "" {
				msg = "the worker sent an embedding that isn't a 768-number unit vector"
			}
			failedIDs, errs = append(failedIDs, r.ID), append(errs, msg)
		}
		w := uuid.NullUUID{UUID: workerID(ctx), Valid: true}
		out := &resultsOutput{}
		out.Body.Saved = facesSaved
		if len(ids) > 0 {
			n, err := s.Store.Q.SaveEmbeddings(ctx, db.SaveEmbeddingsParams{TaskIds: ids, Vectors: vecs, WorkerID: w})
			if err != nil {
				return nil, err
			}
			out.Body.Saved += n
		}
		if len(textIDs) > 0 {
			n, err := s.Store.Q.SaveOCR(ctx, db.SaveOCRParams{TaskIds: textIDs, Texts: texts, WorkerID: w})
			if err != nil {
				return nil, err
			}
			out.Body.Saved += n
		}
		if len(failedIDs) > 0 {
			if err := s.Store.Q.FailTasks(ctx, db.FailTasksParams{TaskIds: failedIDs, Errors: errs, WorkerID: w}); err != nil {
				return nil, err
			}
		}
		out.Body.Failed = len(failedIDs)
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "workerRelease", Method: http.MethodPost, Path: "/api/worker/v1/release", Tags: []string{"Workers"},
		Summary: "Give back tasks a worker won't finish", Security: workerAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *releaseInput) (*struct{}, error) {
		return nil, s.Store.Q.ReleaseTasks(ctx, db.ReleaseTasksParams{TaskIds: in.Body.IDs, WorkerID: uuid.NullUUID{UUID: workerID(ctx), Valid: true}})
	})
}

// saveFaces records what a worker found for a faces task it holds: the item as looked at, and its
// faces (replacing any found before, except ones the owner placed by hand).
func (s *server) saveFaces(ctx context.Context, task int64, found []FaceResult) error {
	type face struct {
		r   FaceResult
		vec string
	}
	var ok []face
	for _, f := range found {
		v, valid := vectorText(f.Embedding, 512)
		if !valid {
			return errors.New("a face's embedding isn't a 512-number unit vector")
		}
		for _, x := range append(append([]float32(nil), f.Box...), f.Landmarks...) {
			if math.IsNaN(float64(x)) || x < -0.5 || x > 1.5 {
				return errors.New("a face's position is out of the picture")
			}
		}
		ok = append(ok, face{f, v})
	}
	w := uuid.NullUUID{UUID: workerID(ctx), Valid: true}
	return s.Store.Tx(ctx, func(q *db.Queries) error {
		done, err := q.FinishFaceTask(ctx, db.FinishFaceTaskParams{TaskID: task, WorkerID: w, Faces: int32(len(ok))})
		if err != nil {
			return errors.New("this worker doesn't hold that task")
		}
		if err := q.ClearFaces(ctx, done.AssetID); err != nil {
			return err
		}
		for _, f := range ok {
			if err := q.InsertFace(ctx, db.InsertFaceParams{AssetID: done.AssetID, Engine: done.Engine, Box: f.r.Box, Landmarks: f.r.Landmarks,
				Score: f.r.Score, Px: f.r.Px, Yaw: f.r.Yaw, Blur: f.r.Blur, Embedding: f.vec}); err != nil {
				return err
			}
		}
		return nil
	})
}

// savePets records what a worker found for a pets task it holds: the item as looked at, and its
// cats and dogs (replacing any found before, except ones the owner placed by hand).
func (s *server) savePets(ctx context.Context, task int64, found []PetResult) error {
	type pet struct {
		r   PetResult
		vec string
	}
	var ok []pet
	for _, p := range found {
		v, valid := vectorText(p.Embedding, 512)
		if !valid {
			return errors.New("an animal's embedding isn't a 512-number unit vector")
		}
		for _, x := range p.Box {
			if math.IsNaN(float64(x)) || x < -0.5 || x > 1.5 {
				return errors.New("an animal's position is out of the picture")
			}
		}
		ok = append(ok, pet{p, v})
	}
	w := uuid.NullUUID{UUID: workerID(ctx), Valid: true}
	return s.Store.Tx(ctx, func(q *db.Queries) error {
		done, err := q.FinishPetTask(ctx, db.FinishPetTaskParams{TaskID: task, WorkerID: w, Pets: int32(len(ok))})
		if err != nil {
			return errors.New("this worker doesn't hold that task")
		}
		if err := q.ClearPets(ctx, done.AssetID); err != nil {
			return err
		}
		for _, p := range ok {
			if err := q.InsertPet(ctx, db.InsertPetParams{AssetID: done.AssetID, Engine: done.Engine, Species: p.r.Species, Box: p.r.Box,
				Score: p.r.Score, Px: p.r.Px, Embedding: p.vec}); err != nil {
				return err
			}
		}
		return nil
	})
}

// taskFrame is a frame from part-way through a video, for its adult task: frame n of videoFrames,
// spread evenly (never the very start or end, which are often blank).
func (s *server) taskFrame(ctx context.Context, asset uuid.UUID, kind string, n int) (*huma.StreamResponse, error) {
	a, err := s.Store.Q.GetAssetForRender(ctx, asset)
	if err != nil || kind != "adult" || a.Type != "video" {
		return nil, huma.Error404NotFound("Only a video's adult task has frames.")
	}
	count := videoFrames(a.DurationMs.Int32)
	if n > count {
		return nil, huma.Error404NotFound("There aren't that many frames.")
	}
	at := float64(a.DurationMs.Int32) / 1000 * float64(n) / float64(count+1)
	frame, err := media.Still(ctx, filepath.Join(a.RootPath, filepath.FromSlash(a.RelPath)), at, 640)
	if err != nil {
		return nil, huma.Error404NotFound("That frame couldn't be read: " + err.Error())
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		hctx.SetHeader("Content-Type", "image/jpeg")
		_, _ = hctx.BodyWriter().Write(frame)
	}}, nil
}

// saveAdult records what a worker's model saw for an adult task it holds, and labels the item
// adult, suggestive or neither (the owner's own corrections stay).
func (s *server) saveAdult(ctx context.Context, task int64, r AdultResult) error {
	if len(r.Scores) != 4*r.Frames {
		return errors.New("adult scores: four for each picture looked at (update the worker)")
	}
	for _, x := range r.Scores {
		if math.IsNaN(float64(x)) || x < 0 || x > 1.0001 {
			return errors.New("adult scores must be probabilities")
		}
	}
	label, score := labels.AdultOf(r.Scores)
	w := uuid.NullUUID{UUID: workerID(ctx), Valid: true}
	return s.Store.Tx(ctx, func(q *db.Queries) error {
		asset, err := q.FinishAdultTask(ctx, db.FinishAdultTaskParams{TaskID: task, WorkerID: w, Scores: r.Scores,
			Frames: int16(r.Frames), Rule: labels.AdultRule})
		if err != nil {
			return errors.New("this worker doesn't hold that task")
		}
		if err := q.ClearScanLabels(ctx, asset); err != nil {
			return err
		}
		if label == "" {
			return nil
		}
		return q.AddScanLabel(ctx, db.AddScanLabelParams{AssetID: asset, Label: label, Score: score})
	})
}

package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"photostead.com/server/internal/store/db"
)

// Sample is an item shown for a prompt or model: its thumbnail and placeholder.
type Sample struct {
	ID        uuid.UUID `json:"id"`
	ThumbHash []byte    `json:"thumbhash,omitempty"`
}

type Prompt struct {
	ID        uuid.UUID `json:"id"`
	Text      string    `json:"text"`
	Uses      int32     `json:"uses" doc:"Items made with it"`
	FirstDate string    `json:"firstDate,omitempty"`
	LastDate  string    `json:"lastDate,omitempty"`
	Samples   []Sample  `json:"samples" doc:"Up to four recent items"`
	Models    []string  `json:"models" doc:"Models it was used with, most used first"`
	Loras     []string  `json:"loras" doc:"LoRAs it was used with, most used first"`
}

type AIModel struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind" enum:"model,lora"`
	Name      string    `json:"name"`
	Uses      int32     `json:"uses"`
	FirstDate string    `json:"firstDate,omitempty"`
	LastDate  string    `json:"lastDate,omitempty"`
	Samples   []Sample  `json:"samples" doc:"Up to four recent items"`
	With      string    `json:"with,omitempty" doc:"For a model, the LoRA used with it most; for a LoRA, the model"`
}

type promptsInput struct {
	Q      string `query:"q" maxLength:"200" doc:"Only prompts containing this text"`
	Sort   string `query:"sort" enum:"uses,recent" default:"uses"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"40"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

type promptsOutput struct {
	Body struct {
		Total   int32    `json:"total" doc:"Prompts matching, in all pages"`
		Prompts []Prompt `json:"prompts"`
	}
}

type promptOutput struct{ Body Prompt }

type modelsOutput struct {
	Body struct {
		Models []AIModel `json:"models" doc:"Checkpoints, diffusion models and services' models, most used first"`
		Loras  []AIModel `json:"loras" doc:"Most used first"`
	}
}

type modelOutput struct{ Body AIModel }

// likePattern matches text containing q, with q's own % and _ taken literally.
func likePattern(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

func (s *server) prompts(ctx context.Context, id uuid.UUID, in promptsInput) ([]Prompt, int32, error) {
	rows, err := s.Store.Q.PromptList(ctx, db.PromptListParams{Viewer: viewer(ctx), ID: id, Pattern: likePattern(in.Q),
		Sort: in.Sort, Lim: in.Limit, Off: in.Offset})
	if err != nil {
		return nil, 0, err
	}
	out := make([]Prompt, len(rows))
	ids := make([]uuid.UUID, len(rows))
	at := map[uuid.UUID]int{}
	var total int32
	for i, r := range rows {
		out[i] = Prompt{ID: r.ID, Text: r.Text, Uses: r.Uses, FirstDate: day(r.FirstDate), LastDate: day(r.LastDate),
			Samples: []Sample{}, Models: []string{}, Loras: []string{}}
		ids[i], at[r.ID], total = r.ID, i, r.Total
	}
	if len(ids) == 0 {
		return out, total, nil
	}
	samples, err := s.Store.Q.PromptSamples(ctx, db.PromptSamplesParams{Viewer: viewer(ctx), Ids: ids, HideAdult: hideAdult(ctx)})
	if err != nil {
		return nil, 0, err
	}
	for _, r := range samples {
		p := &out[at[r.PromptID]]
		p.Samples = append(p.Samples, Sample{ID: r.ID, ThumbHash: r.Thumbhash})
	}
	tools, err := s.Store.Q.PromptTools(ctx, db.PromptToolsParams{Viewer: viewer(ctx), Ids: ids})
	if err != nil {
		return nil, 0, err
	}
	for _, r := range tools {
		p := &out[at[r.PromptID]]
		if r.Kind == "model" {
			p.Models = append(p.Models, r.Display)
		} else {
			p.Loras = append(p.Loras, r.Display)
		}
	}
	return out, total, nil
}

// models lists the models and LoRAs, or just one (id), with its "often with" partner named from
// the whole list.
func (s *server) models(ctx context.Context, id uuid.UUID) ([]AIModel, error) {
	rows, err := s.Store.Q.ModelList(ctx, db.ModelListParams{Viewer: viewer(ctx), ID: uuid.Nil})
	if err != nil {
		return nil, err
	}
	out := make([]AIModel, len(rows))
	ids := make([]uuid.UUID, len(rows))
	at := map[uuid.UUID]int{}
	name := map[uuid.UUID]string{}
	for i, r := range rows {
		out[i] = AIModel{ID: r.ID, Kind: r.Kind, Name: r.Display, Uses: r.Uses, FirstDate: day(r.FirstDate), LastDate: day(r.LastDate), Samples: []Sample{}}
		ids[i], at[r.ID], name[r.ID] = r.ID, i, r.Display
	}
	if len(ids) == 0 {
		return out, nil
	}
	if id != uuid.Nil {
		if _, ok := at[id]; !ok {
			return nil, nil
		}
		ids = []uuid.UUID{id}
	}
	samples, err := s.Store.Q.ModelSamples(ctx, db.ModelSamplesParams{Viewer: viewer(ctx), Ids: ids})
	if err != nil {
		return nil, err
	}
	for _, r := range samples {
		m := &out[at[r.ModelID]]
		m.Samples = append(m.Samples, Sample{ID: r.ID, ThumbHash: r.Thumbhash})
	}
	pairs, err := s.Store.Q.ModelPairs(ctx, viewer(ctx))
	if err != nil {
		return nil, err
	}
	// The partner used most often with each, by name when it's in this list.
	best := map[uuid.UUID]int32{}
	partner := func(a, b uuid.UUID, n int32) {
		i, ok := at[a]
		if !ok || n <= best[a] {
			return
		}
		if nm, ok := name[b]; ok {
			best[a], out[i].With = n, nm
		}
	}
	for _, p := range pairs {
		partner(p.ModelID, p.LoraID, p.Uses)
		partner(p.LoraID, p.ModelID, p.Uses)
	}
	if id != uuid.Nil {
		return []AIModel{out[at[id]]}, nil
	}
	return out, nil
}

func (s *server) registerCreate() {
	huma.Register(s.api, huma.Operation{
		OperationID: "listPrompts", Method: http.MethodGet, Path: "/api/v1/prompts", Tags: []string{"Create"},
		Summary: "The prompt library", Description: "Each distinct prompt once, with its items listed by the timeline endpoints and collection=prompt:{id}.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *promptsInput) (*promptsOutput, error) {
		list, total, err := s.prompts(ctx, uuid.Nil, *in)
		if err != nil {
			return nil, err
		}
		out := &promptsOutput{}
		out.Body.Prompts, out.Body.Total = list, total
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "getPrompt", Method: http.MethodGet, Path: "/api/v1/prompts/{id}", Tags: []string{"Create"},
		Summary: "One prompt", Security: sessionAuth,
	}, func(ctx context.Context, in *idInput) (*promptOutput, error) {
		list, _, err := s.prompts(ctx, in.ID, promptsInput{Sort: "uses", Limit: 1})
		if err != nil {
			return nil, err
		}
		if len(list) == 0 || in.ID == uuid.Nil {
			return nil, huma.Error404NotFound("No such prompt.")
		}
		return &promptOutput{Body: list[0]}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "listModels", Method: http.MethodGet, Path: "/api/v1/models", Tags: []string{"Create"},
		Summary: "Models & LoRAs", Description: "Their items are listed by the timeline endpoints and collection=model:{id} or lora:{id}.",
		Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*modelsOutput, error) {
		list, err := s.models(ctx, uuid.Nil)
		if err != nil {
			return nil, err
		}
		out := &modelsOutput{}
		out.Body.Models, out.Body.Loras = []AIModel{}, []AIModel{}
		for _, m := range list {
			if m.Kind == "model" {
				out.Body.Models = append(out.Body.Models, m)
			} else {
				out.Body.Loras = append(out.Body.Loras, m)
			}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "getModel", Method: http.MethodGet, Path: "/api/v1/models/{id}", Tags: []string{"Create"},
		Summary: "One model or LoRA", Security: sessionAuth,
	}, func(ctx context.Context, in *idInput) (*modelOutput, error) {
		list, err := s.models(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 || in.ID == uuid.Nil {
			return nil, huma.Error404NotFound("No such model or LoRA.")
		}
		return &modelOutput{Body: list[0]}, nil
	})
}

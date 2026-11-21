package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"photostead.com/server/internal/characters"
	"photostead.com/server/internal/store/db"
)

type Character struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Patterns   []string  `json:"patterns" doc:"Patterns for the character's LoRA names (case-insensitive regular expressions)"`
	Count      int32     `json:"count" doc:"Items that show them"`
	FirstDate  string    `json:"firstDate,omitempty"`
	LastDate   string    `json:"lastDate,omitempty"`
	Samples    []Sample  `json:"samples" doc:"Up to four recent items, stills first"`
	Loras      []string  `json:"loras" doc:"The character's own LoRAs that were used, most used first"`
	FaceGroups int32     `json:"faceGroups" doc:"Groups of faces that are this character (from People); their items are the character's too"`
}

type charactersOutput struct {
	Body struct {
		Characters []Character `json:"characters" doc:"Most items first"`
	}
}

type characterOutput struct{ Body Character }

type characterInput struct {
	Body struct {
		Name     string   `json:"name" minLength:"1" maxLength:"100"`
		Patterns []string `json:"patterns" maxItems:"50" doc:"LoRA name patterns; a LoRA picked by name is ^name$"`
	}
}

type characterUpdateInput struct {
	ID uuid.UUID `path:"id"`
	characterInput
}

type removeInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"5000"`
	}
}

func (s *server) characterList(ctx context.Context, id uuid.UUID) ([]Character, error) {
	rows, err := s.Store.Q.ListCharacters(ctx, db.ListCharactersParams{Viewer: viewer(ctx), ID: id})
	if err != nil {
		return nil, err
	}
	out := make([]Character, len(rows))
	ids := make([]uuid.UUID, len(rows))
	at := map[uuid.UUID]int{}
	for i, r := range rows {
		out[i] = Character{ID: r.ID, Name: r.Name, Patterns: r.Loras, Count: r.ItemCount, FirstDate: day(r.FirstDate), LastDate: day(r.LastDate),
			Samples: []Sample{}, Loras: []string{}, FaceGroups: r.FaceGroups}
		ids[i], at[r.ID] = r.ID, i
	}
	if len(ids) == 0 {
		return out, nil
	}
	samples, err := s.Store.Q.CharacterSamples(ctx, db.CharacterSamplesParams{Viewer: viewer(ctx), Ids: ids, HideAdult: hideAdult(ctx)})
	if err != nil {
		return nil, err
	}
	for _, r := range samples {
		c := &out[at[r.CharacterID]]
		c.Samples = append(c.Samples, Sample{ID: r.ID, ThumbHash: r.Thumbhash})
	}
	loras, err := s.Store.Q.CharacterLoras(ctx, db.CharacterLorasParams{Viewer: viewer(ctx), Ids: ids})
	if err != nil {
		return nil, err
	}
	// Name the character's own LoRAs, not the speed or detail LoRAs used alongside them.
	for _, r := range loras {
		c := &out[at[r.CharacterID]]
		if res, err := characters.Compile(c.Patterns); err == nil && characters.Matches(res, r.Display) && len(c.Loras) < 6 {
			c.Loras = append(c.Loras, r.Display)
		}
	}
	return out, nil
}

// cleanPatterns checks a character's LoRA patterns. One known by its faces may have none.
func cleanPatterns(in []string, byFaces bool) ([]string, error) {
	out := []string{}
	for _, p := range in {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 && !byFaces {
		return nil, huma.Error422UnprocessableEntity("Pick at least one LoRA.")
	}
	if _, err := characters.Compile(out); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	return out, nil
}

func duplicateName(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return huma.Error409Conflict("There's already a character with that name.")
	}
	return err
}

func (s *server) registerCharacters() {
	huma.Register(s.api, huma.Operation{
		OperationID: "listCharacters", Method: http.MethodGet, Path: "/api/v1/characters", Tags: []string{"Characters"},
		Summary:     "AI characters",
		Description: "Formed from generation metadata (the character's LoRAs, and videos made from images of them); their items are listed with the timeline endpoints and collection=character:{id}.",
		Security:    sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*charactersOutput, error) {
		list, err := s.characterList(ctx, uuid.Nil)
		if err != nil {
			return nil, err
		}
		out := &charactersOutput{}
		out.Body.Characters = list
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "getCharacter", Method: http.MethodGet, Path: "/api/v1/characters/{id}", Tags: []string{"Characters"},
		Summary: "One character", Security: sessionAuth,
	}, func(ctx context.Context, in *idInput) (*characterOutput, error) {
		list, err := s.characterList(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 || in.ID == uuid.Nil {
			return nil, huma.Error404NotFound("No such character.")
		}
		return &characterOutput{Body: list[0]}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "createCharacter", Method: http.MethodPost, Path: "/api/v1/characters", Tags: []string{"Characters"},
		Summary: "Make a character from their LoRAs", Security: sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *characterInput) (*characterOutput, error) {
		pats, err := cleanPatterns(in.Body.Patterns, false)
		if err != nil {
			return nil, err
		}
		id, err := s.Store.Q.CreateCharacter(ctx, db.CreateCharacterParams{OwnerID: viewer(ctx), Name: strings.TrimSpace(in.Body.Name), Loras: pats})
		if err != nil {
			return nil, duplicateName(err)
		}
		if _, err := characters.Refresh(ctx, s.Store, viewer(ctx)); err != nil {
			return nil, err
		}
		list, err := s.characterList(ctx, id)
		if err != nil || len(list) == 0 {
			return nil, err
		}
		return &characterOutput{Body: list[0]}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "updateCharacter", Method: http.MethodPut, Path: "/api/v1/characters/{id}", Tags: []string{"Characters"},
		Summary: "Rename a character or change their LoRAs", Security: sessionAuth,
	}, func(ctx context.Context, in *characterUpdateInput) (*characterOutput, error) {
		was, err := s.characterList(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		pats, err := cleanPatterns(in.Body.Patterns, len(was) > 0 && was[0].FaceGroups > 0)
		if err != nil {
			return nil, err
		}
		n, err := s.Store.Q.UpdateCharacter(ctx, db.UpdateCharacterParams{Name: strings.TrimSpace(in.Body.Name), Loras: pats, ID: in.ID, OwnerID: viewer(ctx)})
		if err != nil {
			return nil, duplicateName(err)
		}
		if n == 0 {
			return nil, huma.Error404NotFound("No such character.")
		}
		if _, err := characters.Refresh(ctx, s.Store, viewer(ctx)); err != nil {
			return nil, err
		}
		list, err := s.characterList(ctx, in.ID)
		if err != nil || len(list) == 0 {
			return nil, err
		}
		return &characterOutput{Body: list[0]}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "deleteCharacter", Method: http.MethodDelete, Path: "/api/v1/characters/{id}", Tags: []string{"Characters"},
		Summary: "Delete a character", Description: "Only the character goes; the items stay.", Security: sessionAuth,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		n, err := s.Store.Q.DeleteCharacter(ctx, db.DeleteCharacterParams{ID: in.ID, OwnerID: viewer(ctx)})
		if err == nil && n == 0 {
			err = huma.Error404NotFound("No such character.")
		}
		return nil, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "removeFromCharacter", Method: http.MethodPost, Path: "/api/v1/characters/{id}/remove", Tags: []string{"Characters"},
		Summary: "Take items out of a character", Description: "They stay out: matching never adds them back.", Security: sessionAuth,
	}, func(ctx context.Context, in *removeInput) (*characterOutput, error) {
		if _, err := s.Store.Q.SetMemberBasis(ctx, db.SetMemberBasisParams{Basis: "rejected", CharacterID: in.ID, OwnerID: viewer(ctx), AssetIds: in.Body.AssetIDs}); err != nil {
			return nil, err
		}
		list, err := s.characterList(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, huma.Error404NotFound("No such character.")
		}
		return &characterOutput{Body: list[0]}, nil
	})
}

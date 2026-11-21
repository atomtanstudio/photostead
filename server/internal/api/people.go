package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/store/db"
)

// Corrections to people: naming, hiding, merging, and "not this person". Grouping (internal/faces)
// honours them on its next run, which a correction queues.

type personEditInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Name       *string `json:"name,omitempty" maxLength:"200" doc:"Their name; empty leaves them unnamed"`
		Hidden     *bool   `json:"hidden,omitempty" doc:"Hidden people are left out of People and of items' details"`
		InMemories *bool   `json:"inMemories,omitempty" doc:"False keeps them, and every item they're in, out of memories"`
	}
}

type mergeInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Into uuid.UUID `json:"into" doc:"The person to keep; this one's faces and items go to them"`
	}
}

type personItemsInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
	}
}

type faceInput struct {
	ID uuid.UUID `path:"id"`
}

type asCharacterInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Character *uuid.UUID `json:"character,omitempty" doc:"One of your characters"`
		Name      string     `json:"name,omitempty" maxLength:"100" doc:"Or a name: your character of that name, or a new one"`
	}
}

// regroup asks for a grouping run soon (a correction was made).
func (s *server) regroup(ctx context.Context) {
	if s.Jobs != nil {
		_, _ = s.Jobs.Insert(ctx, jobs.FacesArgs{}, nil)
	}
}

func (s *server) onePerson(ctx context.Context, id uuid.UUID) (*personOutput, error) {
	list, err := s.people(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		// Someone with nothing left to show (every item taken out): still theirs, just empty.
		return &personOutput{Body: Person{ID: id}}, nil
	}
	return &personOutput{Body: list[0]}, nil
}

func (s *server) registerPeople() {
	huma.Register(s.api, huma.Operation{
		OperationID: "editPerson", Method: http.MethodPatch, Path: "/api/v1/people/{id}", Tags: []string{"People"},
		Summary: "Name or hide a person, or keep them out of memories", Security: sessionAuth,
	}, func(ctx context.Context, in *personEditInput) (*personOutput, error) {
		p := db.RenamePersonParams{ID: in.ID, OwnerID: viewer(ctx)}
		if in.Body.Name != nil {
			p.Name = pgtype.Text{String: strings.TrimSpace(*in.Body.Name), Valid: true}
		}
		if in.Body.Hidden != nil {
			p.Hidden = pgtype.Bool{Bool: *in.Body.Hidden, Valid: true}
		}
		n, err := s.Store.Q.RenamePerson(ctx, p)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error404NotFound("No such person.")
		}
		if in.Body.InMemories != nil {
			if _, err := s.Store.Q.SetPersonInMemories(ctx, db.SetPersonInMemoriesParams{ID: in.ID, OwnerID: viewer(ctx), InMemories: *in.Body.InMemories}); err != nil {
				return nil, err
			}
		}
		return s.onePerson(ctx, in.ID)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "mergePerson", Method: http.MethodPost, Path: "/api/v1/people/{id}/merge", Tags: []string{"People"},
		Summary:     "Say a person is someone else",
		Description: "Their faces and items go to the other person, who keeps their own name (or takes this one's, if they had none); this person is gone afterwards.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *mergeInput) (*personOutput, error) {
		if in.Body.Into == in.ID {
			return nil, huma.Error422UnprocessableEntity("That's the same person.")
		}
		from, err1 := s.Store.Q.PersonKind(ctx, db.PersonKindParams{ID: in.ID, OwnerID: viewer(ctx)})
		into, err2 := s.Store.Q.PersonKind(ctx, db.PersonKindParams{ID: in.Body.Into, OwnerID: viewer(ctx)})
		if err1 != nil || err2 != nil {
			return nil, huma.Error404NotFound("No such person.")
		}
		if from != into {
			return nil, huma.Error422UnprocessableEntity("A person and a pet, or a cat and a dog, can't be one.")
		}
		var n int64
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			if n, err = q.MergePeople(ctx, db.MergePeopleParams{FromID: in.ID, IntoID: in.Body.Into, OwnerID: viewer(ctx)}); err != nil {
				return err
			}
			return q.SetCovers(ctx, viewer(ctx))
		})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error404NotFound("No such person.")
		}
		s.regroup(ctx)
		return s.onePerson(ctx, in.Body.Into)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "removeFromPerson", Method: http.MethodPost, Path: "/api/v1/people/{id}/remove", Tags: []string{"People"},
		Summary:     "Not this person",
		Description: "Their faces in these items are taken out and kept out; face recognition places them again (perhaps with someone else).",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *personItemsInput) (*changedOutput, error) {
		var n int64
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			if n, err = q.RemoveFromPerson(ctx, db.RemoveFromPersonParams{PersonID: in.ID, OwnerID: viewer(ctx), AssetIds: in.Body.AssetIDs}); err != nil {
				return err
			}
			return q.SetCovers(ctx, viewer(ctx))
		})
		if err != nil {
			return nil, err
		}
		s.regroup(ctx)
		out := &changedOutput{}
		out.Body.Changed = n
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "personToCharacter", Method: http.MethodPost, Path: "/api/v1/people/{id}/character", Tags: []string{"People", "Characters"},
		Summary: "Say a person is a character",
		Description: "For a group of faces that's an AI character rather than someone real: it leaves People, and its items, " +
			"including ones whose faces are grouped with it later, are the character's.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *asCharacterInput) (*characterOutput, error) {
		owner := viewer(ctx)
		name := strings.TrimSpace(in.Body.Name)
		var id uuid.UUID
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			switch {
			case in.Body.Character != nil:
				id = *in.Body.Character
			case name == "":
				return huma.Error422UnprocessableEntity("Pick a character, or name a new one.")
			default:
				found, err := q.CharacterByName(ctx, db.CharacterByNameParams{OwnerID: owner, Name: name})
				if errors.Is(err, pgx.ErrNoRows) {
					found, err = q.CreateCharacter(ctx, db.CreateCharacterParams{OwnerID: owner, Name: name, Loras: []string{}})
				}
				if err != nil {
					return err
				}
				id = found
			}
			n, err := q.LinkCharacter(ctx, db.LinkCharacterParams{CharacterID: uuid.NullUUID{UUID: id, Valid: true}, ID: in.ID, OwnerID: owner})
			if err != nil {
				return err
			}
			if n == 0 {
				return huma.Error404NotFound("No such person or character.")
			}
			return q.SyncFaceCharacters(ctx, owner)
		})
		if err != nil {
			return nil, err
		}
		list, err := s.characterList(ctx, id)
		if err != nil || len(list) == 0 {
			return nil, err
		}
		return &characterOutput{Body: list[0]}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "personFromCharacter", Method: http.MethodDelete, Path: "/api/v1/people/{id}/character", Tags: []string{"People", "Characters"},
		Summary: "Make a group of faces a person again", Security: sessionAuth,
	}, func(ctx context.Context, in *faceInput) (*personOutput, error) {
		owner := viewer(ctx)
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			n, err := q.LinkCharacter(ctx, db.LinkCharacterParams{ID: in.ID, OwnerID: owner})
			if err != nil {
				return err
			}
			if n == 0 {
				return huma.Error404NotFound("No such person.")
			}
			return q.SyncFaceCharacters(ctx, owner)
		})
		if err != nil {
			return nil, err
		}
		return s.onePerson(ctx, in.ID)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "characterFacesToPeople", Method: http.MethodDelete, Path: "/api/v1/characters/{id}/faces", Tags: []string{"Characters", "People"},
		Summary: "Give a character's groups of faces back to People", Description: "Items the character has for other reasons stay.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *faceInput) (*changedOutput, error) {
		owner := viewer(ctx)
		var ids []uuid.UUID
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			if ids, err = q.UnlinkCharacter(ctx, db.UnlinkCharacterParams{CharacterID: in.ID, OwnerID: owner}); err != nil {
				return err
			}
			return q.SyncFaceCharacters(ctx, owner)
		})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = int64(len(ids))
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "getFaceThumbnail", Method: http.MethodGet, Path: "/api/v1/faces/{id}/thumbnail", Tags: []string{"People"},
		Summary: "A face, cropped from its item (WebP, 192px square)", Security: sessionAuth,
		Responses: map[string]*huma.Response{"200": {Description: "WebP image",
			Content: map[string]*huma.MediaType{"image/webp": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
	}, func(ctx context.Context, in *faceInput) (*huma.StreamResponse, error) {
		f, err := s.Store.Q.FaceForThumb(ctx, db.FaceForThumbParams{Viewer: viewer(ctx), ID: in.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("No such face.")
		} else if err != nil {
			return nil, err
		}
		margin := 1.6 // around a face
		if f.Species.Valid {
			margin = 1.1 // around a whole animal
		}
		path, err := s.Render.Face(f.AssetID, in.ID, f.Box, 192, margin)
		if err != nil {
			return nil, huma.Error404NotFound("Not rendered yet.")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			r, w := humago.Unwrap(hctx)
			w.Header().Set("Content-Type", "image/webp")
			w.Header().Set("Cache-Control", "private, max-age=604800")
			http.ServeFile(w, r, path)
		}}, nil
	})
}

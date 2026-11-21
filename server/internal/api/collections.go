package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/faces"
	"photostead.com/server/internal/store/db"
)

type Counts struct {
	All        int32            `json:"all" doc:"Items in the timeline"`
	Images     int32            `json:"images"`
	Videos     int32            `json:"videos"`
	Favorites  int32            `json:"favorites"`
	Archived   int32            `json:"archived"`
	Albums     int32            `json:"albums"`
	People     int32            `json:"people"`
	Prompts    int32            `json:"prompts" doc:"Distinct prompts"`
	Characters int32            `json:"characters"`
	Models     int32            `json:"models" doc:"Models and LoRAs"`
	AI         int32            `json:"ai" doc:"AI-generated or AI-edited items in the timeline"`
	Captured   int32            `json:"captured" doc:"Items with a camera's details"`
	Labels     map[string]int32 `json:"labels" doc:"Items in each utility collection (screenshot, document, receipt, id_card, qr_code), and adult and suggestive items"`
	Trash      int32            `json:"trash" doc:"Items in the trash"`
	LookAlikes int32            `json:"lookAlikes" doc:"Groups of near-duplicates waiting for a yes or no"`
	AdultScan  int32            `json:"adultScan" doc:"Items still waiting to be looked at for adult content"`
}

type countsOutput struct{ Body Counts }

type Album struct {
	ID          uuid.UUID   `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Kind        string      `json:"kind" enum:"manual,smart" doc:"smart: its items are whatever its rules pick, kept up to date"`
	Rules       *SmartRules `json:"rules,omitempty"`
	Parent      *uuid.UUID  `json:"parent,omitempty" doc:"The album it's in"`
	Path        []Ref       `json:"path,omitempty" doc:"The albums it's inside, outermost first"`
	Children    int32       `json:"children,omitempty" doc:"Albums inside it"`
	Count       int32       `json:"count"`
	FirstDate   string      `json:"firstDate,omitempty" doc:"The earliest item's day (YYYY-MM-DD)"`
	LastDate    string      `json:"lastDate,omitempty" doc:"The latest item's day (YYYY-MM-DD)"`
	Cover       *uuid.UUID  `json:"cover,omitempty" doc:"The item to show for it (its thumbnail)"`
	CoverHash   []byte      `json:"coverThumbhash,omitempty"`
	Role        string      `json:"role" enum:"owner,contributor,viewer" doc:"What you may do with it: yours, or shared with you to add to or to see"`
	Owner       *Owner      `json:"owner,omitempty" doc:"Whose it is, when it's shared with you"`
	Members     int32       `json:"members,omitempty" doc:"People it's shared with (for its owner)"`
}

type Person struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name" doc:"Empty for someone face recognition found but nobody has named yet"`
	BirthDate         string     `json:"birthDate,omitempty"`
	Count             int32      `json:"count" doc:"Items they're in"`
	FaceItems         int32      `json:"faceItems" doc:"Of those, items where their face was recognized (the rest are another app's suggestions, for items not yet looked at for faces)"`
	Origin            string     `json:"origin" enum:"immich,faces,user" doc:"Where they came from: an import, face recognition, or you"`
	Kind              string     `json:"kind" enum:"human,pet"`
	Species           string     `json:"species,omitempty" enum:"cat,dog" doc:"For a pet"`
	Hidden            bool       `json:"hidden,omitempty"`
	KeptOutOfMemories bool       `json:"keptOutOfMemories,omitempty" doc:"Left out of memories, with every item they're in"`
	CoverFace         *uuid.UUID `json:"coverFace,omitempty" doc:"The face to show for them (/api/v1/faces/{id}/thumbnail)"`
	Cover             *uuid.UUID `json:"cover,omitempty" doc:"Without a face yet: the item to show for them"`
	CoverHash         []byte     `json:"coverThumbhash,omitempty"`
}

type albumsOutput struct {
	Body struct {
		Albums []Album `json:"albums" doc:"Newest first"`
	}
}

type albumOutput struct{ Body Album }

type peopleOutput struct {
	Body struct {
		People []Person     `json:"people" doc:"The named first, then by most items"`
		Faces  FaceProgress `json:"faces" doc:"How far face recognition has got"`
	}
}

type FaceProgress struct {
	Scanned int32 `json:"scanned" doc:"Items looked at for faces"`
	Waiting int32 `json:"waiting" doc:"Items waiting to be looked at"`
}

type personOutput struct{ Body Person }

type peopleInput struct {
	Hidden bool `query:"hidden" doc:"List the people you've hidden instead"`
}

type idInput struct {
	ID uuid.UUID `path:"id"`
}

func day(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}

func cover(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func (s *server) albums(ctx context.Context, id uuid.UUID) ([]Album, error) {
	if id == uuid.Nil {
		return nil, nil
	}
	list, err := s.albumList(ctx, db.ListAlbumsParams{Viewer: viewer(ctx), ID: id})
	if err != nil || len(list) == 0 {
		return list, err
	}
	rows, err := s.Store.Q.AlbumPath(ctx, db.AlbumPathParams{ID: id, Viewer: viewer(ctx)})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		list[0].Path = append(list[0].Path, Ref{ID: r.ID, Name: r.Title})
	}
	return list, nil
}

// albumList lists albums; a smart album's count, dates and cover come from its rules. With every
// album (a picker's list), each gets its path.
func (s *server) albumList(ctx context.Context, p db.ListAlbumsParams) ([]Album, error) {
	rows, err := s.Store.Q.ListAlbums(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]Album, len(rows))
	byID := map[uuid.UUID]int{}
	for i, r := range rows {
		a := Album{ID: r.ID, Title: r.Title, Description: r.Description, Kind: r.Kind, Count: r.ItemCount, Children: r.Children,
			FirstDate: day(r.FirstDate), LastDate: day(r.LastDate), Cover: cover(r.CoverID), CoverHash: r.CoverThumbhash, Role: "owner", Members: r.Members}
		if r.OwnerID != viewer(ctx) {
			a.Role, a.Owner, a.Members, a.Children = r.MyRole, &Owner{ID: r.OwnerID, Name: r.OwnerName}, 0, 0
		}
		if r.ParentID.Valid {
			id := r.ParentID.UUID
			a.Parent = &id
		}
		if r.Kind == "smart" {
			a.Rules = &SmartRules{}
			if len(r.Query) > 0 {
				if err := json.Unmarshal(r.Query, a.Rules); err != nil {
					return nil, err
				}
			}
			if err := s.fillSmart(ctx, &a); err != nil {
				return nil, err
			}
		}
		out[i], byID[r.ID] = a, i
	}
	if p.Everything {
		for i := range out {
			for at := out[i].Parent; at != nil; {
				j, ok := byID[*at]
				if !ok || len(out[i].Path) > 32 {
					break
				}
				out[i].Path = append([]Ref{{ID: out[j].ID, Name: out[j].Title}}, out[i].Path...)
				at = out[j].Parent
			}
		}
	}
	return out, nil
}

func (s *server) people(ctx context.Context, id uuid.UUID, hidden bool) ([]Person, error) {
	rows, err := s.Store.Q.ListPeople(ctx, db.ListPeopleParams{Viewer: viewer(ctx), ID: id, Hidden: hidden})
	if err != nil {
		return nil, err
	}
	out := make([]Person, len(rows))
	for i, r := range rows {
		out[i] = Person{ID: r.ID, Name: r.Name, BirthDate: day(r.BirthDate), Count: r.ItemCount, FaceItems: r.FaceItems,
			Origin: r.Origin, Kind: r.Kind, Species: r.Species.String, Hidden: r.Hidden, KeptOutOfMemories: !r.InMemories, CoverFace: cover(r.CoverFaceID), Cover: cover(r.CoverID),
			CoverHash: r.CoverThumbhash}
	}
	return out, nil
}

func (s *server) registerCollections() {
	huma.Register(s.api, huma.Operation{
		OperationID: "libraryCounts", Method: http.MethodGet, Path: "/api/v1/library/counts", Tags: []string{"Timeline"},
		Summary: "How many items, favorites, archived items, albums and people there are", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*countsOutput, error) {
		r, err := s.Store.Q.LibraryCounts(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		c := Counts{All: r.AllItems, Images: r.Images, Videos: r.Videos, Favorites: r.Favorites, Archived: r.Archived, Albums: r.Albums,
			People: r.People, Prompts: r.Prompts, Models: r.Models, Characters: r.Characters, AI: r.Ai, Captured: r.Captured, Labels: map[string]int32{}, Trash: r.Trash}
		if err := json.Unmarshal(r.Labels, &c.Labels); err != nil {
			return nil, err
		}
		if c.LookAlikes, err = s.Store.Q.SuggestedStackCount(ctx, viewer(ctx)); err != nil {
			return nil, err
		}
		if n, err := s.Store.Q.AdultWaiting(ctx); err == nil {
			c.AdultScan = n
		}
		return &countsOutput{Body: c}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "getAlbum", Method: http.MethodGet, Path: "/api/v1/albums/{id}", Tags: []string{"Albums"},
		Summary: "One album", Security: sessionAuth,
	}, func(ctx context.Context, in *idInput) (*albumOutput, error) {
		list, err := s.albums(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 || in.ID == uuid.Nil {
			return nil, huma.Error404NotFound("No such album.")
		}
		return &albumOutput{Body: list[0]}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "listPeople", Method: http.MethodGet, Path: "/api/v1/people", Tags: []string{"People"},
		Summary: "People", Description: "Everyone you've named, then the people face recognition found but nobody has named yet. " +
			"An item is theirs when their face is recognized in it; for items not yet looked at for faces, an import's suggestions stand in. " +
			"List their items with the timeline endpoints and collection=person:{id}.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *peopleInput) (*peopleOutput, error) {
		list, err := s.people(ctx, uuid.Nil, in.Hidden)
		if err != nil {
			return nil, err
		}
		out := &peopleOutput{}
		out.Body.People = list
		if p, err := s.Store.Q.FaceProgress(ctx, faces.Engine); err == nil {
			out.Body.Faces = FaceProgress{Scanned: p.Scanned, Waiting: p.Waiting}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "getPerson", Method: http.MethodGet, Path: "/api/v1/people/{id}", Tags: []string{"People"},
		Summary: "One person", Security: sessionAuth,
	}, func(ctx context.Context, in *idInput) (*personOutput, error) {
		list, err := s.people(ctx, in.ID, false)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 || in.ID == uuid.Nil {
			return nil, huma.Error404NotFound("No such person.")
		}
		return &personOutput{Body: list[0]}, nil
	})
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store/db"
)

// Albums inside albums, smart albums (saved filters: their items are whatever their rules pick,
// kept up to date), and a read-only view of the folders on disk.

// SmartRules pick a smart album's items from the timeline. Every rule left out matches everything.
type SmartRules struct {
	Type      string      `json:"type,omitempty" enum:"image,video"`
	Favorite  bool        `json:"favorite,omitempty" doc:"Only favorites"`
	MinRating int32       `json:"rating,omitempty" minimum:"0" maximum:"5" doc:"At least this many stars"`
	Flag      string      `json:"flag,omitempty" enum:"pick,reject,unrejected"`
	Label     string      `json:"label,omitempty" enum:"red,yellow,green,blue,purple"`
	People    []uuid.UUID `json:"people,omitempty" maxItems:"10" doc:"All of these people (or pets) are in it"`
	Keywords  []string    `json:"keywords,omitempty" maxItems:"20" doc:"Any of these keywords"`
	After     string      `json:"after,omitempty" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"Taken on this day or later"`
	Before    string      `json:"before,omitempty" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"Taken on this day or earlier"`
	Source    string      `json:"source,omitempty" enum:"ai,not_ai,camera" doc:"Made with AI, not made with AI, or taken with a camera or phone"`
}

// filter narrows the timeline: the library's own filters, or a smart album's rules (with the
// viewer's filters on top).
type filter struct {
	Type      string
	Hide      []string
	MinRating int32
	Flag      string
	Color     string
	Favorite  bool
	People    []uuid.UUID
	Keywords  []string
	After     pgtype.Date
	Before    pgtype.Date
	Source    string
	Library   string // mine, household, or both ("")
}

func parseDay(s string) pgtype.Date {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return pgtype.Date{Time: t, Valid: true}
	}
	return pgtype.Date{}
}

func (r SmartRules) filter() filter {
	f := filter{Type: r.Type, Hide: []string{}, MinRating: r.MinRating, Flag: r.Flag, Color: r.Label, Favorite: r.Favorite,
		People: r.People, Keywords: []string{}, After: parseDay(r.After), Before: parseDay(r.Before), Source: r.Source}
	if f.People == nil {
		f.People = []uuid.UUID{}
	}
	for _, k := range r.Keywords {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			f.Keywords = append(f.Keywords, k)
		}
	}
	return f
}

// narrow adds the viewer's own choices (type, marks) to a filter.
func (f filter) narrow(typ string, m MarksFilter) filter {
	if typ != "" {
		f.Type = typ
	}
	f.MinRating = max(f.MinRating, m.MinRating)
	if m.Flag != "" {
		f.Flag = m.Flag
	}
	if m.Label != "" {
		f.Color = m.Label
	}
	return f
}

func (f filter) buckets(viewer uuid.UUID) db.TimelineBucketsParams {
	return db.TimelineBucketsParams{Viewer: viewer, Type: f.Type, Hide: f.Hide, MinRating: f.MinRating, Flag: f.Flag, Color: f.Color,
		Favorite: f.Favorite, People: f.People, Keywords: f.Keywords, After: f.After, Before: f.Before, Source: f.Source, Library: f.Library}
}

func (f filter) month(viewer uuid.UUID, from time.Time) db.TimelineRangeParams {
	return db.TimelineRangeParams{Viewer: viewer, Type: f.Type, FromDate: date(from), ToDate: date(from.AddDate(0, 1, 0)), Hide: f.Hide,
		MinRating: f.MinRating, Flag: f.Flag, Color: f.Color, Favorite: f.Favorite, People: f.People, Keywords: f.Keywords,
		After: f.After, Before: f.Before, Source: f.Source, Library: f.Library}
}

// timelineFilter is the library timeline's filter from a request.
func timelineFilter(typ string, hideUtility bool, library string, m MarksFilter) filter {
	return filter{Hide: hidden(hideUtility), People: []uuid.UUID{}, Keywords: []string{}, Library: library}.narrow(typ, m)
}

// smartFilter is a smart album's filter, if the album (one of the viewer's) is smart.
func (s *server) smartFilter(ctx context.Context, id uuid.UUID) (filter, bool, error) {
	r, err := s.Store.Q.AlbumRules(ctx, db.AlbumRulesParams{ID: id, Viewer: viewer(ctx)})
	if errors.Is(err, pgx.ErrNoRows) {
		return filter{}, false, nil
	}
	if err != nil || r.Kind != "smart" {
		return filter{}, false, err
	}
	var rules SmartRules
	if len(r.Query) > 0 {
		if err := json.Unmarshal(r.Query, &rules); err != nil {
			return filter{}, false, err
		}
	}
	return rules.filter(), true, nil
}

// fillSmart works out a smart album's count, dates and cover from its rules.
func (s *server) fillSmart(ctx context.Context, a *Album) error {
	if a.Rules == nil {
		return nil
	}
	f := a.Rules.filter()
	months, err := s.Store.Q.TimelineBuckets(ctx, f.buckets(viewer(ctx)))
	if err != nil || len(months) == 0 {
		return err
	}
	a.Count = 0
	for _, m := range months {
		a.Count += m.Count
	}
	newest, _ := time.Parse("2006-01", months[0].Month)
	oldest, _ := time.Parse("2006-01", months[len(months)-1].Month)
	top, err := s.Store.Q.TimelineRange(ctx, f.month(viewer(ctx), newest))
	if err != nil {
		return err
	}
	bottom, err := s.Store.Q.TimelineRange(ctx, f.month(viewer(ctx), oldest))
	if err != nil {
		return err
	}
	if len(top) > 0 {
		a.LastDate = top[0].LocalDate.Time.Format("2006-01-02")
		for _, x := range top {
			if x.RenderState == "done" {
				id := x.ID
				a.Cover, a.CoverHash = &id, x.Thumbhash
				break
			}
		}
	}
	if len(bottom) > 0 {
		a.FirstDate = bottom[len(bottom)-1].LocalDate.Time.Format("2006-01-02")
	}
	return nil
}

type albumListInput struct {
	Parent string `query:"parent" doc:"The albums inside this one (otherwise those at the top)"`
	All    bool   `query:"all" doc:"Every album, at any depth, each with its path (for a picker)"`
}

type albumEditInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Title       *string     `json:"title,omitempty" minLength:"1" maxLength:"200"`
		Description *string     `json:"description,omitempty" maxLength:"4000"`
		Rules       *SmartRules `json:"rules,omitempty" doc:"A smart album's rules"`
	}
}

type albumMoveInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Parent *uuid.UUID `json:"parent,omitempty" doc:"The album to put it in; left out, it goes to the top"`
	}
}

type Folder struct {
	Name  string `json:"name"`
	Path  string `json:"path" doc:"Within its library, without a trailing slash"`
	Items int32  `json:"items" doc:"Photos and videos anywhere under it"`
}

type Library struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind" enum:"managed,indexed" doc:"managed: Photostead's own share; indexed: another folder it reads"`
	ReadOnly bool      `json:"readOnly"`
	Items    int32     `json:"items"`
}

type foldersOutput struct {
	Body struct {
		Libraries []Library `json:"libraries"`
	}
}

type folderInput struct {
	ID   uuid.UUID `path:"id"`
	Path string    `query:"path" maxLength:"2000" doc:"A folder in the library; empty for its top"`
}

type folderOutput struct {
	Body struct {
		Library Library  `json:"library"`
		Path    string   `json:"path"`
		Folders []Folder `json:"folders" doc:"The folders directly inside, by name"`
	}
}

// folderPrefix turns "a/b" into "a/b/" (and "" into ""), refusing anything that climbs out.
func folderPrefix(p string) (string, error) {
	p = strings.Trim(p, "/")
	if p == "" {
		return "", nil
	}
	if strings.Contains(p, "\\") || path.Clean(p) != p || strings.HasPrefix(p, "..") {
		return "", huma.Error422UnprocessableEntity("That isn't a folder path.")
	}
	return p + "/", nil
}

func (s *server) registerAlbums() {
	huma.Register(s.api, huma.Operation{
		OperationID: "listAlbums", Method: http.MethodGet, Path: "/api/v1/albums", Tags: []string{"Albums"},
		Summary:     "Albums",
		Description: "The albums at the top, or inside one (parent), or every album (all). Their items are listed with the timeline endpoints and collection=album:{id}.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *albumListInput) (*albumsOutput, error) {
		p := db.ListAlbumsParams{Viewer: viewer(ctx), Everything: in.All}
		if in.Parent != "" {
			id, err := uuid.Parse(in.Parent)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("parent must be an album's id")
			}
			p.Parent = id
		}
		list, err := s.albumList(ctx, p)
		if err != nil {
			return nil, err
		}
		out := &albumsOutput{}
		out.Body.Albums = list
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "editAlbum", Method: http.MethodPatch, Path: "/api/v1/albums/{id}", Tags: []string{"Albums"},
		Summary: "Rename an album, describe it, or change a smart album's rules", Security: sessionAuth,
	}, func(ctx context.Context, in *albumEditInput) (*albumOutput, error) {
		p := db.UpdateAlbumParams{ID: in.ID, Owner: viewer(ctx)}
		if in.Body.Title != nil {
			p.Title = pgtype.Text{String: strings.TrimSpace(*in.Body.Title), Valid: true}
		}
		if in.Body.Description != nil {
			p.Description = pgtype.Text{String: strings.TrimSpace(*in.Body.Description), Valid: true}
		}
		if in.Body.Rules != nil {
			b, err := json.Marshal(in.Body.Rules)
			if err != nil {
				return nil, err
			}
			p.Query = b
		}
		n, err := s.Store.Q.UpdateAlbum(ctx, p)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error404NotFound("No such album.")
		}
		return s.oneAlbum(ctx, in.ID)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "moveAlbum", Method: http.MethodPost, Path: "/api/v1/albums/{id}/move", Tags: []string{"Albums"},
		Summary: "Put an album inside another, or at the top", Security: sessionAuth,
	}, func(ctx context.Context, in *albumMoveInput) (*albumOutput, error) {
		p := db.MoveAlbumParams{ID: in.ID, Owner: viewer(ctx)}
		if in.Body.Parent != nil {
			p.ParentID = uuid.NullUUID{UUID: *in.Body.Parent, Valid: true}
		}
		n, err := s.Store.Q.MoveAlbum(ctx, p)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error422UnprocessableEntity("It can't go there: an album can't go inside itself or one of its own.")
		}
		return s.oneAlbum(ctx, in.ID)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "deleteAlbum", Method: http.MethodDelete, Path: "/api/v1/albums/{id}", Tags: []string{"Albums"},
		Summary: "Delete an album", Description: "Only the album goes: its items stay in the library, and albums inside it move up to where it was.",
		Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		var n int64
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			if err := q.LiftChildAlbums(ctx, db.LiftChildAlbumsParams{ID: in.ID, Owner: viewer(ctx)}); err != nil {
				return err
			}
			var err error
			n, err = q.DeleteAlbum(ctx, db.DeleteAlbumParams{ID: in.ID, Owner: viewer(ctx)})
			return err
		})
		if err == nil && n == 0 {
			err = huma.Error404NotFound("No such album.")
		}
		return nil, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "listLibraries", Method: http.MethodGet, Path: "/api/v1/folders", Tags: []string{"Folders"},
		Summary: "Your libraries, to browse by folder", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*foldersOutput, error) {
		rows, err := s.Store.Q.FolderLibraries(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		out := &foldersOutput{}
		out.Body.Libraries = make([]Library, len(rows))
		for i, r := range rows {
			out.Body.Libraries[i] = Library{ID: r.ID, Name: r.Name, Kind: r.Kind, ReadOnly: r.ReadOnly, Items: r.Files}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "listFolder", Method: http.MethodGet, Path: "/api/v1/folders/{id}", Tags: []string{"Folders"},
		Summary:     "The folders in one of a library's folders",
		Description: "Read-only: the folders on disk as they are. The items directly in a folder are listed with the timeline endpoints, collection=folder:{library id} and folder={path}.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *folderInput) (*folderOutput, error) {
		prefix, err := folderPrefix(in.Path)
		if err != nil {
			return nil, err
		}
		libs, err := s.Store.Q.FolderLibraries(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		out := &folderOutput{}
		found := false
		for _, l := range libs {
			if l.ID == in.ID {
				out.Body.Library, found = Library{ID: l.ID, Name: l.Name, Kind: l.Kind, ReadOnly: l.ReadOnly, Items: l.Files}, true
			}
		}
		if !found {
			return nil, huma.Error404NotFound("No such library.")
		}
		rows, err := s.Store.Q.ListFolders(ctx, db.ListFoldersParams{Folder: prefix, LibraryID: in.ID, Viewer: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		out.Body.Path = strings.TrimSuffix(prefix, "/")
		out.Body.Folders = make([]Folder, len(rows))
		for i, r := range rows {
			out.Body.Folders[i] = Folder{Name: r.Name, Path: prefix + r.Name, Items: r.Files}
		}
		return out, nil
	})
}

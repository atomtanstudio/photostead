package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/store/db"
)

// Talking about a shared album, and choosing from it. Anyone an album is shared with (household
// members, and guests through their personal links) can comment on it or on an item in it, and
// pick items: each person's own picks, as a photographer's client chooses from a proofing album.
// The owner sees who picked what. Comments, and who added what, make the album's activity.

type Comment struct {
	ID        uuid.UUID  `json:"id"`
	AssetID   *uuid.UUID `json:"assetId,omitempty" doc:"The item it's about; none: the album"`
	Author    string     `json:"author"`
	Guest     bool       `json:"guest,omitempty"`
	Mine      bool       `json:"mine" doc:"Yours: you may delete it (the album's owner may delete any)"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Activity struct {
	Kind    string      `json:"kind" enum:"added,comment"`
	ID      *uuid.UUID  `json:"id,omitempty" doc:"The comment's (comment)"`
	Mine    bool        `json:"mine,omitempty" doc:"A comment you may take back: yours, or any in your album"`
	Who     string      `json:"who"`
	At      time.Time   `json:"at"`
	Items   int32       `json:"items,omitempty" doc:"How many were added (added)"`
	Assets  []uuid.UUID `json:"assets,omitempty" doc:"A few of them, newest first (added)"`
	Body    string      `json:"body,omitempty" doc:"What was said (comment)"`
	AssetID *uuid.UUID  `json:"assetId,omitempty" doc:"The item it's about (comment)"`
}

type Pick struct {
	AssetID uuid.UUID `json:"assetId"`
	People  []string  `json:"people" doc:"Who picked it"`
	Mine    bool      `json:"mine"`
}

type commentsInput struct {
	ID    uuid.UUID `path:"id"`
	Asset string    `query:"asset" doc:"Only the comments about this item"`
}

type newCommentInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Body    string     `json:"body" minLength:"1" maxLength:"2000"`
		AssetID *uuid.UUID `json:"assetId,omitempty" doc:"An item in the album; none: about the album"`
	}
}

type commentIDInput struct {
	ID      uuid.UUID `path:"id"`
	Comment uuid.UUID `path:"comment"`
}

type pickInput struct {
	ID    uuid.UUID `path:"id"`
	Asset uuid.UUID `path:"asset"`
}

type commentsOutput struct {
	Body struct {
		Comments []Comment `json:"comments"`
	}
}

type commentOutput struct{ Body Comment }

type activityOutput struct {
	Body struct {
		Activity []Activity `json:"activity"`
	}
}

type picksOutput struct {
	Body struct {
		Picks []Pick `json:"picks"`
	}
}

// talkRole is what the viewer may do in an album, if it's theirs or shared with them.
func (s *server) talkRole(ctx context.Context, album uuid.UUID) (db.AlbumRoleRow, error) {
	role, err := s.Store.Q.AlbumRole(ctx, db.AlbumRoleParams{AlbumID: album, Viewer: viewer(ctx)})
	if errors.Is(err, pgx.ErrNoRows) {
		return role, huma.Error404NotFound("No such album.")
	}
	return role, err
}

func (s *server) registerTalk() {
	huma.Register(s.api, huma.Operation{
		OperationID: "albumActivity", Method: http.MethodGet, Path: "/api/v1/albums/{id}/activity", Tags: []string{"Sharing"},
		Summary: "What happened in an album: who added what, and comments", Security: sessionAuth,
	}, func(ctx context.Context, in *albumIDInput) (*activityOutput, error) {
		role, err := s.talkRole(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		me := viewer(ctx)
		rows, err := s.Store.Q.AlbumActivity(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		out := &activityOutput{}
		out.Body.Activity = make([]Activity, 0, len(rows))
		for _, r := range rows {
			a := Activity{Kind: r.Kind, Who: r.Who, At: r.At, Items: r.Items, Body: r.Body}
			if r.Kind == "added" {
				a.Assets = r.Assets
			}
			if r.AssetID.Valid {
				a.AssetID = &r.AssetID.UUID
			}
			if r.CommentID.Valid {
				a.ID = &r.CommentID.UUID
				a.Mine = r.WhoID.Valid && r.WhoID.UUID == me || role.OwnerID == me
			}
			out.Body.Activity = append(out.Body.Activity, a)
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "albumComments", Method: http.MethodGet, Path: "/api/v1/albums/{id}/comments", Tags: []string{"Sharing"},
		Summary: "An album's comments (about one item, or all), oldest first", Security: sessionAuth,
	}, func(ctx context.Context, in *commentsInput) (*commentsOutput, error) {
		role, err := s.talkRole(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var asset uuid.NullUUID
		if in.Asset != "" {
			id, err := uuid.Parse(in.Asset)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("asset isn't an id.")
			}
			asset = uuid.NullUUID{UUID: id, Valid: true}
		}
		rows, err := s.Store.Q.AlbumComments(ctx, db.AlbumCommentsParams{AlbumID: in.ID, AssetID: asset})
		if err != nil {
			return nil, err
		}
		out := &commentsOutput{}
		out.Body.Comments = make([]Comment, 0, len(rows))
		me := viewer(ctx)
		for _, r := range rows {
			c := Comment{ID: r.ID, Author: r.Author, Guest: r.AuthorRole == "guest", Mine: r.AuthorID == me || role.OwnerID == me, Body: r.Body, CreatedAt: r.CreatedAt}
			if r.AssetID.Valid {
				c.AssetID = &r.AssetID.UUID
			}
			out.Body.Comments = append(out.Body.Comments, c)
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "addAlbumComment", Method: http.MethodPost, Path: "/api/v1/albums/{id}/comments", Tags: []string{"Sharing"},
		Summary: "Say something about an album, or an item in it", Security: sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *newCommentInput) (*commentOutput, error) {
		if _, err := s.talkRole(ctx, in.ID); err != nil {
			return nil, err
		}
		body := strings.TrimSpace(in.Body.Body)
		if body == "" {
			return nil, huma.Error422UnprocessableEntity("Say something first.")
		}
		var asset uuid.NullUUID
		if in.Body.AssetID != nil {
			asset = uuid.NullUUID{UUID: *in.Body.AssetID, Valid: true}
		}
		row, err := s.Store.Q.AddAlbumComment(ctx, db.AddAlbumCommentParams{AlbumID: in.ID, AssetID: asset, AuthorID: viewer(ctx), Body: body})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("That item isn't in this album.")
		} else if err != nil {
			return nil, err
		}
		me, _ := s.Store.Q.GetUser(ctx, viewer(ctx))
		return &commentOutput{Body: Comment{ID: row.ID, AssetID: in.Body.AssetID, Author: firstNonEmpty(me.DisplayName, strings.Split(me.Email, "@")[0]),
			Guest: me.Role == "guest", Mine: true, Body: body, CreatedAt: row.CreatedAt}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "deleteAlbumComment", Method: http.MethodDelete, Path: "/api/v1/albums/{id}/comments/{comment}", Tags: []string{"Sharing"},
		Summary: "Take a comment back (yours; the album's owner may take back any)", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *commentIDInput) (*struct{}, error) {
		c, err := s.Store.Q.AlbumComment(ctx, in.Comment)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && c.AlbumID != in.ID {
			return nil, huma.Error404NotFound("No such comment.")
		} else if err != nil {
			return nil, err
		}
		if me := viewer(ctx); c.AuthorID != me && c.OwnerID != me {
			return nil, huma.Error403Forbidden("Only who said it, or the album's owner, can take it back.")
		}
		return nil, s.Store.Q.DeleteAlbumComment(ctx, in.Comment)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "albumPicks", Method: http.MethodGet, Path: "/api/v1/albums/{id}/picks", Tags: []string{"Sharing"},
		Summary:     "Who picked what in an album",
		Description: "Everyone it's shared with may pick items (proofing). The owner sees everyone's picks; others see their own, and how many picked each.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *albumIDInput) (*picksOutput, error) {
		role, err := s.talkRole(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		rows, err := s.Store.Q.AlbumPicks(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		me := viewer(ctx)
		owner := role.OwnerID == me
		by := map[uuid.UUID]*Pick{}
		var order []uuid.UUID
		for _, r := range rows {
			p := by[r.AssetID]
			if p == nil {
				p = &Pick{AssetID: r.AssetID, People: []string{}}
				by[r.AssetID] = p
				order = append(order, r.AssetID)
			}
			if r.UserID == me {
				p.Mine = true
			}
			if owner || r.UserID == me {
				p.People = append(p.People, r.Who)
			}
		}
		out := &picksOutput{}
		out.Body.Picks = make([]Pick, 0, len(order))
		for _, id := range order {
			out.Body.Picks = append(out.Body.Picks, *by[id])
		}
		return out, nil
	})

	pick := func(on bool) func(context.Context, *pickInput) (*struct{}, error) {
		return func(ctx context.Context, in *pickInput) (*struct{}, error) {
			if _, err := s.talkRole(ctx, in.ID); err != nil {
				return nil, err
			}
			p := db.SetAlbumPickParams{AlbumID: in.ID, AssetID: in.Asset, UserID: viewer(ctx)}
			if on {
				return nil, s.Store.Q.SetAlbumPick(ctx, p)
			}
			return nil, s.Store.Q.RemoveAlbumPick(ctx, db.RemoveAlbumPickParams(p))
		}
	}
	huma.Register(s.api, huma.Operation{
		OperationID: "pickAlbumItem", Method: http.MethodPut, Path: "/api/v1/albums/{id}/picks/{asset}", Tags: []string{"Sharing"},
		Summary: "Pick an item in an album (yours, for the owner to see)", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, pick(true))
	huma.Register(s.api, huma.Operation{
		OperationID: "unpickAlbumItem", Method: http.MethodDelete, Path: "/api/v1/albums/{id}/picks/{asset}", Tags: []string{"Sharing"},
		Summary: "Unpick an item in an album", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, pick(false))
}

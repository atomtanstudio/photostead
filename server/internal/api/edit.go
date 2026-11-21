package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/store/db"
	"photostead.com/server/internal/trash"
)

// Edits to a person's items: the trash, favorites and albums.

type itemsInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
	}
}

type changedOutput struct {
	Body struct {
		Changed int64 `json:"changed" doc:"Items changed (others weren't yours, or were already that way)"`
	}
}

type emptyInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds,omitempty" maxItems:"10000" doc:"Items to delete for good; none means the whole trash"`
	}
}

type emptyOutput struct {
	Body struct {
		Deleted int `json:"deleted" doc:"Items deleted for good"`
		Kept    int `json:"kept" doc:"Items left in the trash because a file couldn't be deleted"`
	}
}

type favoriteInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
		Favorite bool        `json:"favorite"`
	}
}

type marksInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
		Rating   *int16      `json:"rating,omitempty" minimum:"0" maximum:"5" doc:"Stars, 1-5; 0 takes them off"`
		Flag     *string     `json:"flag,omitempty" enum:"pick,reject,none"`
		Label    *string     `json:"label,omitempty" enum:"red,yellow,green,blue,purple,none"`
	}
}

type archiveInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
		Archived bool        `json:"archived" doc:"true: out of the timeline into the archive; false: back"`
	}
}

type keywordsInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
		Add      []string    `json:"add,omitempty" maxItems:"50" doc:"Keywords to put on them (a new name makes a new keyword)"`
		Remove   []string    `json:"remove,omitempty" maxItems:"50" doc:"Keywords to take off them"`
	}
}

type keywordsOutput struct {
	Body struct {
		Added   int64 `json:"added" doc:"Keywords put on items"`
		Removed int64 `json:"removed" doc:"Keywords taken off items"`
	}
}

type Keyword struct {
	Name  string `json:"name"`
	Items int32  `json:"items"`
}

type keywordListOutput struct {
	Body struct {
		Keywords []Keyword `json:"keywords" doc:"Most used first"`
	}
}

type idsInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
	}
}

type descriptionInput struct {
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
		Text     string      `json:"text" maxLength:"8000" doc:"The description; empty hides any from the file or an import"`
	}
}

type dateInput struct {
	Body struct {
		AssetIDs     []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
		ShiftSeconds int64       `json:"shiftSeconds,omitempty" doc:"Move them all by this much (negative is earlier)"`
		SetEarliest  string      `json:"setEarliest,omitempty" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}(:[0-9]{2})?$" doc:"Or: the date and time (as the clock read, YYYY-MM-DDTHH:MM) for the earliest of them; the rest move by the same amount"`
	}
}

type dateOutput struct {
	Body struct {
		Changed      int   `json:"changed" doc:"Items moved, with their Live Photo clips and stacked versions"`
		ShiftSeconds int64 `json:"shiftSeconds" doc:"How far they moved (to undo, move them back)"`
	}
}

func keywordList(rows []db.SelectionKeywordsRow) []Keyword {
	out := make([]Keyword, len(rows))
	for i, r := range rows {
		out[i] = Keyword{Name: r.Name, Items: r.Items}
	}
	return out
}

type newAlbumInput struct {
	Body struct {
		Title    string      `json:"title" minLength:"1" maxLength:"200"`
		AssetIDs []uuid.UUID `json:"assetIds,omitempty" maxItems:"10000" doc:"Items to put in it"`
		Parent   *uuid.UUID  `json:"parent,omitempty" doc:"The album to make it in; otherwise it's at the top"`
		Rules    *SmartRules `json:"rules,omitempty" doc:"Makes a smart album: its items are whatever these pick, kept up to date"`
	}
}

type albumItemsInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		AssetIDs []uuid.UUID `json:"assetIds" minItems:"1" maxItems:"10000"`
	}
}

func (s *server) registerEdits() {
	huma.Register(s.api, huma.Operation{
		OperationID: "trashItems", Method: http.MethodPost, Path: "/api/v1/assets/trash", Tags: []string{"Trash"},
		Summary:     "Move items to the trash",
		Description: "With a Live Photo's clip and the rest of a stack it tops. Items are deleted for good 30 days later, or when the trash is emptied.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *itemsInput) (*changedOutput, error) {
		ids, err := s.Store.Q.TrashAssets(ctx, db.TrashAssetsParams{Ids: in.Body.AssetIDs, Owner: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		if err := s.Store.Q.ReplaceTrashedCovers(ctx, viewer(ctx)); err != nil { // a stack keeps the rest of it
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = int64(len(ids))
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "restoreItems", Method: http.MethodPost, Path: "/api/v1/assets/restore", Tags: []string{"Trash"},
		Summary: "Take items out of the trash", Security: sessionAuth,
	}, func(ctx context.Context, in *itemsInput) (*changedOutput, error) {
		ids, err := s.Store.Q.RestoreAssets(ctx, db.RestoreAssetsParams{Ids: in.Body.AssetIDs, Owner: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = int64(len(ids))
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "emptyTrash", Method: http.MethodPost, Path: "/api/v1/trash/empty", Tags: []string{"Trash"},
		Summary: "Delete items in the trash for good",
		Description: "Photostead deletes its own copies (in the photostead share) and its renders. Copies in read-only folders it indexes " +
			"are left alone, and the item never comes back from them.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *emptyInput) (*emptyOutput, error) {
		st, err := trash.Empty(ctx, s.Store, s.Render, viewer(ctx), in.Body.AssetIDs, false, slog.Default())
		if err != nil {
			return nil, err
		}
		out := &emptyOutput{}
		out.Body.Deleted, out.Body.Kept = st.Deleted, st.Kept
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "favoriteItems", Method: http.MethodPost, Path: "/api/v1/assets/favorite", Tags: []string{"Collections"},
		Summary: "Make items favorites, or not", Security: sessionAuth,
	}, func(ctx context.Context, in *favoriteInput) (*changedOutput, error) {
		n, err := s.Store.Q.SetFavorites(ctx, db.SetFavoritesParams{Viewer: viewer(ctx), Favorite: in.Body.Favorite, Ids: in.Body.AssetIDs})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = n
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "archiveItems", Method: http.MethodPost, Path: "/api/v1/assets/archive", Tags: []string{"Collections"},
		Summary:     "Archive your items, or bring them back",
		Description: "Out of the timeline (and the Household's) into the archive, with their Live Photo clips and the RAW of a RAW+JPEG pair; albums keep them. Only your own, and not ones in the vault or the trash.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *archiveInput) (*changedOutput, error) {
		n, err := s.Store.Q.ArchiveItems(ctx, db.ArchiveItemsParams{Owner: viewer(ctx), Archived: in.Body.Archived, Ids: in.Body.AssetIDs})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = n
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "markItems", Method: http.MethodPost, Path: "/api/v1/assets/marks", Tags: []string{"Collections"},
		Summary:     "Rate, flag or label items",
		Description: "Stars, pick/reject flags and color labels are the viewer's own, like favorites. Fields left out stay as they are.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *marksInput) (*changedOutput, error) {
		b := in.Body
		if b.Rating == nil && b.Flag == nil && b.Label == nil {
			return nil, huma.Error422UnprocessableEntity("Give a rating, a flag or a label.")
		}
		var n int64
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			if b.Rating != nil {
				if n, err = q.SetRatings(ctx, db.SetRatingsParams{Viewer: viewer(ctx), Ids: b.AssetIDs,
					Rating: pgtype.Int2{Int16: *b.Rating, Valid: *b.Rating > 0}}); err != nil {
					return err
				}
			}
			if b.Flag != nil {
				if n, err = q.SetFlags(ctx, db.SetFlagsParams{Viewer: viewer(ctx), Ids: b.AssetIDs,
					Flag: pgtype.Text{String: *b.Flag, Valid: *b.Flag != "none"}}); err != nil {
					return err
				}
			}
			if b.Label != nil {
				if n, err = q.SetColorLabels(ctx, db.SetColorLabelsParams{Viewer: viewer(ctx), Ids: b.AssetIDs,
					Color: pgtype.Text{String: *b.Label, Valid: *b.Label != "none"}}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = n
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "editKeywords", Method: http.MethodPost, Path: "/api/v1/assets/keywords", Tags: []string{"Edit"},
		Summary: "Put keywords on items, or take them off", Description: "Only your own items. Names are matched ignoring case.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *keywordsInput) (*keywordsOutput, error) {
		out := &keywordsOutput{}
		err := s.Store.Tx(ctx, func(q *db.Queries) error {
			var err error
			if len(in.Body.Add) > 0 {
				if out.Body.Added, err = q.AddKeywords(ctx, db.AddKeywordsParams{Owner: viewer(ctx), Ids: in.Body.AssetIDs, Names: in.Body.Add}); err != nil {
					return err
				}
			}
			if len(in.Body.Remove) > 0 {
				if out.Body.Removed, err = q.RemoveKeywords(ctx, db.RemoveKeywordsParams{Owner: viewer(ctx), Ids: in.Body.AssetIDs, Names: in.Body.Remove}); err != nil {
					return err
				}
			}
			return nil
		})
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "selectionKeywords", Method: http.MethodPost, Path: "/api/v1/assets/keywords/list", Tags: []string{"Edit"},
		Summary: "The keywords on these items, with how many have each", Security: sessionAuth,
	}, func(ctx context.Context, in *idsInput) (*keywordListOutput, error) {
		rows, err := s.Store.Q.SelectionKeywords(ctx, db.SelectionKeywordsParams{Owner: viewer(ctx), Ids: in.Body.AssetIDs})
		if err != nil {
			return nil, err
		}
		out := &keywordListOutput{}
		out.Body.Keywords = keywordList(rows)
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "listKeywords", Method: http.MethodGet, Path: "/api/v1/keywords", Tags: []string{"Edit"},
		Summary: "Your keywords, most used first", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*keywordListOutput, error) {
		rows, err := s.Store.Q.ListKeywords(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		out := &keywordListOutput{}
		out.Body.Keywords = make([]Keyword, len(rows))
		for i, r := range rows {
			out.Body.Keywords[i] = Keyword{Name: r.Name, Items: r.Items}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setDescription", Method: http.MethodPost, Path: "/api/v1/assets/description", Tags: []string{"Edit"},
		Summary: "Write a description for items", Description: "Only your own items. It's shown instead of the file's or an import's.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *descriptionInput) (*changedOutput, error) {
		n, err := s.Store.Q.SetDescriptions(ctx, db.SetDescriptionsParams{Owner: viewer(ctx), Ids: in.Body.AssetIDs, Text: strings.TrimSpace(in.Body.Text)})
		if err != nil {
			return nil, err
		}
		out := &changedOutput{}
		out.Body.Changed = n
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setDates", Method: http.MethodPost, Path: "/api/v1/assets/date", Tags: []string{"Edit"},
		Summary: "Move items in time",
		Description: "Only your own items, with their Live Photo clips and stacked versions. Either move them all by an amount, or set when " +
			"the earliest was taken and move the rest with it, so their spacing is kept (a camera clock that was off). Their files in the photostead share then move to the new day's folder.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *dateInput) (*dateOutput, error) {
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		zone := auth.EffectiveZone(u, s.ServerZone)
		if zone == "" {
			zone = "UTC"
		}
		shift := in.Body.ShiftSeconds
		if in.Body.SetEarliest != "" {
			layout := "2006-01-02T15:04"
			if len(in.Body.SetEarliest) > len(layout) {
				layout += ":05"
			}
			want, err := time.Parse(layout, in.Body.SetEarliest)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("setEarliest must look like 2024-03-05T14:30")
			}
			wall, err := s.Store.Q.EarliestWallTime(ctx, db.EarliestWallTimeParams{Owner: viewer(ctx), Ids: in.Body.AssetIDs, Zone: zone})
			if err != nil {
				return nil, huma.Error404NotFound("None of those are yours.")
			}
			shift = int64(want.Sub(wall.Time) / time.Second)
		}
		out := &dateOutput{}
		out.Body.ShiftSeconds = shift
		if shift == 0 {
			return out, nil
		}
		ids, err := s.Store.Q.ShiftDates(ctx, db.ShiftDatesParams{Owner: viewer(ctx), Ids: in.Body.AssetIDs, Seconds: float64(shift), Zone: zone})
		if err != nil {
			return nil, err
		}
		out.Body.Changed = len(ids)
		if s.Jobs != nil && len(ids) > 0 { // their files follow, into the new day's folder
			if _, err := s.Jobs.Insert(ctx, jobs.RefileArgs{OwnerID: viewer(ctx), AssetIDs: ids}, nil); err != nil {
				return nil, err
			}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "createAlbum", Method: http.MethodPost, Path: "/api/v1/albums", Tags: []string{"Albums"},
		Summary: "Make an album (optionally with items in it, or inside another), or a smart album", Security: sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *newAlbumInput) (*albumOutput, error) {
		p := db.NewAlbumParams{Owner: viewer(ctx), Title: strings.TrimSpace(in.Body.Title), Kind: "manual"}
		if in.Body.Parent != nil {
			p.ParentID = uuid.NullUUID{UUID: *in.Body.Parent, Valid: true}
		}
		if in.Body.Rules != nil {
			b, err := json.Marshal(in.Body.Rules)
			if err != nil {
				return nil, err
			}
			p.Kind, p.Query = "smart", b
		}
		id, err := s.Store.Q.NewAlbum(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("No such album to make it in.")
		}
		if err != nil {
			return nil, err
		}
		if len(in.Body.AssetIDs) > 0 && in.Body.Rules == nil {
			if _, err := s.Store.Q.AddToAlbum(ctx, db.AddToAlbumParams{Viewer: viewer(ctx), AlbumID: id, Ids: in.Body.AssetIDs}); err != nil {
				return nil, err
			}
		}
		return s.oneAlbum(ctx, id)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "addToAlbum", Method: http.MethodPost, Path: "/api/v1/albums/{id}/add", Tags: []string{"Albums"},
		Summary: "Put items in an album", Security: sessionAuth,
	}, func(ctx context.Context, in *albumItemsInput) (*albumOutput, error) {
		if _, err := s.Store.Q.AddToAlbum(ctx, db.AddToAlbumParams{Viewer: viewer(ctx), AlbumID: in.ID, Ids: in.Body.AssetIDs}); err != nil {
			return nil, err
		}
		_ = s.Store.Q.TouchAlbum(ctx, db.TouchAlbumParams{AlbumID: in.ID, Viewer: viewer(ctx)})
		return s.oneAlbum(ctx, in.ID)
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "removeFromAlbum", Method: http.MethodPost, Path: "/api/v1/albums/{id}/remove", Tags: []string{"Albums"},
		Summary: "Take items out of an album (they stay in the library)", Security: sessionAuth,
	}, func(ctx context.Context, in *albumItemsInput) (*albumOutput, error) {
		if _, err := s.Store.Q.RemoveFromAlbum(ctx, db.RemoveFromAlbumParams{Viewer: viewer(ctx), AlbumID: in.ID, Ids: in.Body.AssetIDs}); err != nil {
			return nil, err
		}
		_ = s.Store.Q.TouchAlbum(ctx, db.TouchAlbumParams{AlbumID: in.ID, Viewer: viewer(ctx)})
		return s.oneAlbum(ctx, in.ID)
	})
}

func (s *server) oneAlbum(ctx context.Context, id uuid.UUID) (*albumOutput, error) {
	list, err := s.albums(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, huma.Error404NotFound("No such album.")
	}
	return &albumOutput{Body: list[0]}, nil
}

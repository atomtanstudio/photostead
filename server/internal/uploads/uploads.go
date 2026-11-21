// Package uploads applies what came with an upload to its item: a description (with the source
// link, so a saved post can always be found again), keywords, an album, and from importers a
// favorite, the archive, stars, where it was taken and an AI item's prompt. It runs as soon as the
// item is known: right away when the file was already in the library, otherwise when it's indexed.
package uploads

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/provenance"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Extras is what an upload brings besides the file.
type Extras struct {
	Description string
	SourceURL   string
	Keywords    []string
	Album       string // an album's title: found, or made
	Favorite    bool
	Archived    bool
	Rating      int16    // stars, 1-5 (0: none given)
	Lat, Lon    *float64 // where it was taken, kept only when the file doesn't say
	Prompt      string   // what an AI item was asked for, shown only when its file doesn't say
}

// Text is the description to keep: the given one, and the source link unless it already says it.
func (e Extras) Text() string {
	d := strings.TrimSpace(e.Description)
	if u := strings.TrimSpace(e.SourceURL); u != "" && !strings.Contains(d, u) {
		if d != "" {
			d += "\n\n"
		}
		d += u
	}
	return d
}

// Apply gives an owner's item what came with its upload.
func Apply(ctx context.Context, s *store.Store, owner, asset uuid.UUID, e Extras) error {
	return s.Tx(ctx, func(q *db.Queries) error {
		if t := e.Text(); t != "" {
			if err := q.AddUploadDescription(ctx, db.AddUploadDescriptionParams{AssetID: asset, Text: t}); err != nil {
				return err
			}
		}
		var names []string
		for _, k := range e.Keywords {
			if k = strings.TrimSpace(k); k != "" {
				names = append(names, k)
			}
		}
		if len(names) > 0 {
			rows, err := q.EnsureKeywords(ctx, db.EnsureKeywordsParams{OwnerID: owner, Names: names})
			if err != nil {
				return err
			}
			ids := make([]uuid.UUID, len(rows))
			assets := make([]uuid.UUID, len(rows))
			for i, r := range rows {
				ids[i], assets[i] = r.ID, asset
			}
			if _, err := q.AddAssetKeywords(ctx, db.AddAssetKeywordsParams{AssetIds: assets, KeywordIds: ids, Source: "upload"}); err != nil {
				return err
			}
		}
		if title := strings.TrimSpace(e.Album); title != "" {
			album, err := q.AlbumByTitle(ctx, db.AlbumByTitleParams{OwnerID: owner, Title: title})
			if errors.Is(err, pgx.ErrNoRows) {
				album, err = q.NewAlbum(ctx, db.NewAlbumParams{Owner: owner, Title: title, Kind: "manual"})
			}
			if err != nil {
				return err
			}
			if _, err := q.AddToAlbum(ctx, db.AddToAlbumParams{Viewer: owner, AlbumID: album, Ids: []uuid.UUID{asset}}); err != nil {
				return err
			}
		}
		if e.Favorite {
			if _, err := q.SetFavorites(ctx, db.SetFavoritesParams{Viewer: owner, Favorite: true, Ids: []uuid.UUID{asset}}); err != nil {
				return err
			}
		}
		if e.Rating > 0 {
			if _, err := q.SetRatings(ctx, db.SetRatingsParams{Viewer: owner, Ids: []uuid.UUID{asset}, Rating: pgtype.Int2{Int16: e.Rating, Valid: true}}); err != nil {
				return err
			}
		}
		if e.Archived {
			if _, err := q.ArchiveItems(ctx, db.ArchiveItemsParams{Owner: owner, Archived: true, Ids: []uuid.UUID{asset}}); err != nil {
				return err
			}
		}
		if e.Lat != nil && e.Lon != nil {
			if err := q.SetUploadedLocation(ctx, db.SetUploadedLocationParams{AssetID: asset, Lat: pgtype.Float8{Float64: *e.Lat, Valid: true},
				Lon: pgtype.Float8{Float64: *e.Lon, Valid: true}}); err != nil {
				return err
			}
		}
		return provenance.Give(ctx, q, asset, owner, e.Prompt)
	})
}

// AddToAlbum puts an owner's items in their album with this title, making it if need be.
func AddToAlbum(ctx context.Context, s *store.Store, owner uuid.UUID, title string, ids []uuid.UUID) error {
	title = strings.TrimSpace(title)
	if title == "" || len(ids) == 0 {
		return nil
	}
	return s.Tx(ctx, func(q *db.Queries) error {
		album, err := q.AlbumByTitle(ctx, db.AlbumByTitleParams{OwnerID: owner, Title: title})
		if errors.Is(err, pgx.ErrNoRows) {
			album, err = q.NewAlbum(ctx, db.NewAlbumParams{Owner: owner, Title: title, Kind: "manual"})
		}
		if err != nil {
			return err
		}
		_, err = q.AddToAlbum(ctx, db.AddToAlbumParams{Viewer: owner, AlbumID: album, Ids: ids})
		return err
	})
}

// ApplyWaiting applies the uploads waiting for a file, now that it belongs to an item.
func ApplyWaiting(ctx context.Context, s *store.Store, file, asset uuid.UUID) error {
	rows, err := s.Q.WaitingUploads(ctx, uuid.NullUUID{UUID: file, Valid: true})
	if err != nil {
		return err
	}
	for _, u := range rows {
		e := Extras{Description: u.Description, SourceURL: u.SourceUrl, Keywords: u.Keywords, Album: u.Album, Favorite: u.Favorite, Archived: u.Archived,
			Rating: u.Rating.Int16, Prompt: u.Prompt}
		if u.Lat.Valid && u.Lon.Valid {
			e.Lat, e.Lon = &u.Lat.Float64, &u.Lon.Float64
		}
		if err := Apply(ctx, s, u.OwnerID, asset, e); err != nil {
			return err
		}
		if err := s.Q.UploadApplied(ctx, db.UploadAppliedParams{ID: u.ID, AssetID: uuid.NullUUID{UUID: asset, Valid: true}}); err != nil {
			return err
		}
	}
	return nil
}

package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/store/db"
)

// The sync feed (GET /api/v1/sync/stream), for the phone apps: everything the person may see, as
// JSON Lines, then only what changed. Without since (or with one too old, or from a database
// restored since), it sends every item and album and ends with a checkpoint; the phone keeps the
// checkpoint's cursor and next time asks ?since=cursor, getting the items and albums changed since
// then: each again as it is now, or gone (no longer there, or no longer theirs to see). Lines:
//
//	{"type":"reset"}                                  start over: forget what you have (then everything follows)
//	{"type":"asset","asset":{…}}                      an item, as it is now
//	{"type":"assetGone","id":"…"}                     an item to forget
//	{"type":"album","album":{…,"assetIds":[…]}}       an album and its items, in order
//	{"type":"albumGone","id":"…"}                     an album to forget
//	{"type":"checkpoint","cursor":"…"}                the last line: where to carry on from
//
// A feed that stops before its checkpoint was cut short: ask again from the old cursor. An item
// can come twice; apply it again. An item gone leaves every album it's in on the phone (its albums
// aren't sent again for it). One that's gone may never have been the phone's (another
// person's, changed). Items in the library (yours and the Household's) have inLibrary; the others
// are in albums shared with you. Hidden ones (a Live Photo's clip, an item under a stack's cover)
// have stacked or motionOf. Adult items have adult: the phone hides or blurs them by the person's
// setting (GET /api/v1/auth/me), as the web does.

// SyncAsset is an item in the sync feed.
type SyncAsset struct {
	ID         uuid.UUID  `json:"id"`
	Type       string     `json:"type"`
	TakenAt    time.Time  `json:"takenAt"`
	OffsetMin  *int16     `json:"offsetMin,omitempty" doc:"Minutes from UTC where it was taken, when known"`
	LocalDate  string     `json:"localDate"`
	Width      int32      `json:"width"`
	Height     int32      `json:"height"`
	DurationMs int32      `json:"durationMs,omitempty"`
	ThumbHash  []byte     `json:"thumbhash,omitempty"`
	Render     string     `json:"render"`
	MotionID   *uuid.UUID `json:"motionId,omitempty"`
	MotionOf   *uuid.UUID `json:"motionOf,omitempty" doc:"A Live Photo's clip: of this photo (not shown on its own)"`
	OwnerID    uuid.UUID  `json:"ownerId"`
	Visibility string     `json:"visibility" doc:"timeline or archived"`
	AI         string     `json:"ai,omitempty"`
	SHA256     string     `json:"sha256" doc:"The original's, to match a phone's own photos"`
	Mime       string     `json:"mime,omitempty"`
	FileName   string     `json:"fileName,omitempty"`
	Favorite   bool       `json:"favorite,omitempty"`
	Rating     int16      `json:"rating,omitempty"`
	Flag       string     `json:"flag,omitempty"`
	Label      string     `json:"label,omitempty"`
	Shared     bool       `json:"shared,omitempty" doc:"In the Household space"`
	InLibrary  bool       `json:"inLibrary,omitempty" doc:"In your library (yours, or shared with the Household); else only in an album shared with you"`
	StackSize  int32      `json:"stackSize,omitempty"`
	Stacked    bool       `json:"stacked,omitempty" doc:"Under another item, a stack's cover (not shown on its own)"`
	Adult      bool       `json:"adult,omitempty" doc:"Adult content (nudity or sex), by a scan or its owner: the phone blurs or hides it by the person's setting"`
}

// SyncAlbum is an album in the sync feed, with its items.
type SyncAlbum struct {
	ID          uuid.UUID   `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	OwnerID     uuid.UUID   `json:"ownerId"`
	ParentID    *uuid.UUID  `json:"parentId,omitempty"`
	Kind        string      `json:"kind" doc:"manual, or smart (its items aren't listed: ask the API)"`
	CoverID     *uuid.UUID  `json:"coverId,omitempty"`
	Role        string      `json:"role" doc:"owner, contributor or viewer"`
	UpdatedAt   time.Time   `json:"updatedAt"`
	AssetIDs    []uuid.UUID `json:"assetIds"`
}

type syncLine struct {
	Type   string     `json:"type"`
	Asset  *SyncAsset `json:"asset,omitempty"`
	Album  *SyncAlbum `json:"album,omitempty"`
	ID     *uuid.UUID `json:"id,omitempty"`
	Cursor string     `json:"cursor,omitempty"`
}

const (
	syncPage      = 2000
	syncSettingID = "sync" // {"prunedThrough": "<xid8>"}: changes up to there were cleared (jobs.PruneSync)
)

func syncAssetOf(r db.SyncAssetsRow) SyncAsset {
	a := SyncAsset{ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: r.LocalDate.Time.Format("2006-01-02"), Width: r.Width.Int32,
		Height: r.Height.Int32, DurationMs: r.DurationMs.Int32, ThumbHash: r.Thumbhash, Render: r.RenderState, OwnerID: r.OwnerID,
		Visibility: r.Visibility, AI: aiKind(r.SourceType), SHA256: r.Sha256, Mime: r.Mime.String, Favorite: r.Favorite, Rating: r.Rating.Int16,
		Flag: r.Flag.String, Label: r.ColorLabel.String, Shared: r.Shared, InLibrary: r.InLibrary, StackSize: r.StackSize, Stacked: r.Stacked, Adult: r.Adult}
	if r.TakenOffsetMin.Valid {
		a.OffsetMin = &r.TakenOffsetMin.Int16
	}
	if r.LiveMotionID.Valid {
		a.MotionID = &r.LiveMotionID.UUID
	}
	if r.MotionOf.Valid {
		a.MotionOf = &r.MotionOf.UUID
	}
	if r.RelPath != "" {
		a.FileName = path.Base(r.RelPath)
	}
	return a
}

func (s *server) registerSync(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/sync/stream", s.syncStream)
}

func (s *server) syncStream(w http.ResponseWriter, r *http.Request) {
	viewer, _, ok := s.uploader(r) // a session, a paired device or an API key
	if !ok {
		replyError(w, http.StatusUnauthorized, "Sign in, pair the device, or send an API key.")
		return
	}
	since := r.URL.Query().Get("since")
	if since != "" {
		if _, err := strconv.ParseUint(since, 10, 64); err != nil {
			replyError(w, http.StatusBadRequest, "since is the cursor from a checkpoint.")
			return
		}
	}
	ctx := r.Context()
	// One snapshot for the whole read, so its cursor and what it sends agree.
	tx, err := s.Store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	q := s.Store.Q.WithTx(tx)
	cursor, err := q.SyncCursor(ctx)
	if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	reset := since == "" || s.syncTooOld(ctx, q, since, cursor)

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	var out io.Writer = w
	var gz *gzip.Writer
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gz, _ = gzip.NewWriterLevel(w, gzip.BestSpeed)
		defer gz.Close()
		out = gz
	}
	enc := json.NewEncoder(out)
	lines := 0
	emit := func(l syncLine) error {
		if err := enc.Encode(l); err != nil {
			return err
		}
		if lines++; lines%1000 == 0 { // keep it moving: a phone sees progress, and a proxy a live connection
			if gz != nil {
				_ = gz.Flush()
			}
			_ = http.NewResponseController(w).Flush()
		}
		return nil
	}
	if err := s.syncSend(ctx, q, viewer, since, reset, emit); err != nil {
		return // cut short: no checkpoint, so the phone asks again from its old cursor
	}
	_ = emit(syncLine{Type: "checkpoint", Cursor: cursor})
}

// syncTooOld: changes the phone needs were cleared away, or the cursor is from ahead of this
// database (restored from a backup since).
func (s *server) syncTooOld(ctx context.Context, q *db.Queries, since, cursor string) bool {
	from, _ := strconv.ParseUint(since, 10, 64)
	now, _ := strconv.ParseUint(cursor, 10, 64)
	if from > now {
		return true
	}
	b, err := q.ServerSetting(ctx, syncSettingID)
	if err != nil {
		return false
	}
	var st struct {
		PrunedThrough string `json:"prunedThrough"`
	}
	_ = json.Unmarshal(b, &st)
	pruned, _ := strconv.ParseUint(st.PrunedThrough, 10, 64)
	return from <= pruned
}

func (s *server) syncSend(ctx context.Context, q *db.Queries, viewer uuid.UUID, since string, reset bool, emit func(syncLine) error) error {
	if reset {
		if since != "" {
			if err := emit(syncLine{Type: "reset"}); err != nil {
				return err
			}
		}
		for after := uuid.Nil; ; {
			rows, err := q.SyncAssets(ctx, db.SyncAssetsParams{Viewer: viewer, Ids: []uuid.UUID{}, After: after, Lim: syncPage})
			if err != nil {
				return err
			}
			for _, r := range rows {
				a := syncAssetOf(r)
				if err := emit(syncLine{Type: "asset", Asset: &a}); err != nil {
					return err
				}
			}
			if len(rows) < syncPage {
				break
			}
			after = rows[len(rows)-1].ID
		}
		return s.syncAlbums(ctx, q, viewer, []uuid.UUID{}, emit)
	}
	changed, err := q.SyncChanged(ctx, since)
	if err != nil {
		return err
	}
	var assets, albums []uuid.UUID
	for _, c := range changed {
		if c.Kind == "asset" {
			assets = append(assets, c.ID)
		} else {
			albums = append(albums, c.ID)
		}
	}
	for i := 0; i < len(assets); i += syncPage {
		ids := assets[i:min(i+syncPage, len(assets))]
		rows, err := q.SyncAssets(ctx, db.SyncAssetsParams{Viewer: viewer, Ids: ids, After: uuid.Nil, Lim: int32(len(ids))})
		if err != nil {
			return err
		}
		seen := make(map[uuid.UUID]bool, len(rows))
		for _, r := range rows {
			seen[r.ID] = true
			a := syncAssetOf(r)
			if err := emit(syncLine{Type: "asset", Asset: &a}); err != nil {
				return err
			}
		}
		for _, id := range ids {
			if !seen[id] {
				if err := emit(syncLine{Type: "assetGone", ID: &id}); err != nil {
					return err
				}
			}
		}
	}
	if len(albums) > 0 {
		return s.syncAlbums(ctx, q, viewer, albums, emit)
	}
	return nil
}

// syncAlbums sends these albums (all the viewer's, when ids is empty), each with its items, and
// those of ids that aren't the viewer's any more as gone.
func (s *server) syncAlbums(ctx context.Context, q *db.Queries, viewer uuid.UUID, ids []uuid.UUID, emit func(syncLine) error) error {
	albums, err := q.SyncAlbums(ctx, db.SyncAlbumsParams{Viewer: viewer, Ids: ids})
	if err != nil {
		return err
	}
	have := make([]uuid.UUID, 0, len(albums))
	for _, al := range albums {
		have = append(have, al.ID)
	}
	items := map[uuid.UUID][]uuid.UUID{}
	if len(have) > 0 {
		rows, err := q.SyncAlbumItems(ctx, db.SyncAlbumItemsParams{Viewer: viewer, Ids: have})
		if err != nil {
			return err
		}
		for _, r := range rows {
			items[r.AlbumID] = append(items[r.AlbumID], r.AssetID)
		}
	}
	seen := map[uuid.UUID]bool{}
	for _, al := range albums {
		seen[al.ID] = true
		a := SyncAlbum{ID: al.ID, Title: al.Title, Description: al.Description, OwnerID: al.OwnerID, Kind: al.Kind, Role: al.Role,
			UpdatedAt: al.UpdatedAt, AssetIDs: items[al.ID]}
		if a.AssetIDs == nil {
			a.AssetIDs = []uuid.UUID{}
		}
		if al.ParentID.Valid {
			a.ParentID = &al.ParentID.UUID
		}
		if al.CoverAssetID.Valid {
			a.CoverID = &al.CoverAssetID.UUID
		}
		if err := emit(syncLine{Type: "album", Album: &a}); err != nil {
			return err
		}
	}
	for _, id := range ids {
		if !seen[id] {
			if err := emit(syncLine{Type: "albumGone", ID: &id}); err != nil {
				return err
			}
		}
	}
	return nil
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/labels"
	"photostead.com/server/internal/provenance"
	"photostead.com/server/internal/store/db"
)

type Bucket struct {
	Month string `json:"month" example:"2026-10" doc:"Year and month (YYYY-MM), by the local day each item was taken"`
	Count int32  `json:"count"`
}

type bucketsOutput struct {
	Body struct {
		Total   int64    `json:"total"`
		Buckets []Bucket `json:"buckets" doc:"Newest month first"`
	}
}

type TimelineAsset struct {
	ID         uuid.UUID  `json:"id"`
	Type       string     `json:"type" enum:"image,video"`
	TakenAt    time.Time  `json:"takenAt"`
	LocalDate  string     `json:"localDate" doc:"The day it was taken, in the place it was taken (YYYY-MM-DD)"`
	Width      int32      `json:"width" doc:"Display width after orientation; 0 when unknown"`
	Height     int32      `json:"height"`
	DurationMs int32      `json:"durationMs,omitempty"`
	ThumbHash  []byte     `json:"thumbhash,omitempty" doc:"ThumbHash placeholder, base64"`
	Render     string     `json:"render" enum:"pending,done,failed"`
	MotionID   *uuid.UUID `json:"motionId,omitempty" doc:"For a Live Photo, its motion clip (play it from /assets/{motionId}/original)"`
	StackSize  int32      `json:"stackSize,omitempty" doc:"For the top item of a stack (such as RAW+JPEG), how many items it holds"`
	Favorite   bool       `json:"favorite,omitempty"`
	AI         string     `json:"ai,omitempty" enum:"generated,edited" doc:"Made (or changed) by a generative model, with evidence in the file"`
	Rating     int16      `json:"rating,omitempty" doc:"The viewer's stars, 1-5"`
	Flag       string     `json:"flag,omitempty" enum:"pick,reject"`
	Label      string     `json:"label,omitempty" enum:"red,yellow,green,blue,purple" doc:"The viewer's color label"`
	Shared     bool       `json:"shared,omitempty" doc:"In the Household space"`
	FromOthers bool       `json:"fromOthers,omitempty" doc:"Another member's, shared with the Household"`
	Adult      bool       `json:"adult,omitempty" doc:"Adult content (nudity or sex), by a scan or its owner: blurred for a viewer who asked for that"`
	Copy       bool       `json:"copy,omitempty" doc:"A video with a copy every browser plays (/assets/{id}/play): smaller, for previews"`
}

// MarksFilter narrows the timeline or a collection by the viewer's stars, flags and labels.
type MarksFilter struct {
	MinRating int32  `query:"rating" minimum:"0" maximum:"5" doc:"At least this many stars"`
	Flag      string `query:"flag" enum:"pick,reject,unrejected" doc:"Only picks, only rejects, or everything but rejects"`
	Label     string `query:"label" enum:"red,yellow,green,blue,purple" doc:"Only items with this color label"`
}

// marks fills in an item's stars, flag and label from nullable columns.
func (a *TimelineAsset) marks(rating pgtype.Int2, flag, label pgtype.Text) {
	a.Rating, a.Flag, a.Label = rating.Int16, flag.String, label.String
}

// addMarks fills in the viewer's favorites, stars, flags and labels on a list read without them.
func (s *server) addMarks(ctx context.Context, list []TimelineAsset) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(list))
	at := map[uuid.UUID][]int{}
	for i, a := range list {
		ids[i] = a.ID
		at[a.ID] = append(at[a.ID], i)
	}
	rows, err := s.Store.Q.AssetMarks(ctx, db.AssetMarksParams{Viewer: viewer(ctx), Ids: ids})
	if err != nil {
		return err
	}
	for _, r := range rows {
		for _, i := range at[r.AssetID] {
			list[i].Favorite = list[i].Favorite || r.Favorite
			list[i].marks(r.Rating, r.Flag, r.ColorLabel)
		}
	}
	return nil
}

// adultAmong is which of these items are adult.
func (s *server) adultAmong(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.Store.Q.AdultAmong(ctx, ids)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	set := make(map[uuid.UUID]bool, len(rows))
	for _, id := range rows {
		set[id] = true
	}
	return set, nil
}

// markAdult flags a list's adult items, leaving them out instead when drop is set.
func (s *server) markAdult(ctx context.Context, list []TimelineAsset, drop bool) ([]TimelineAsset, error) {
	ids := make([]uuid.UUID, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	adult, err := s.adultAmong(ctx, ids)
	if err != nil || len(adult) == 0 {
		return list, err
	}
	out := make([]TimelineAsset, 0, len(list))
	for _, a := range list {
		if adult[a.ID] {
			if drop {
				continue
			}
			a.Adult = true
		}
		out = append(out, a)
	}
	return out, nil
}

// sift finishes a list of results: adult items flagged (or left out, for a viewer who hides them),
// then the viewer's favorites, stars, flags and labels filled in.
func (s *server) sift(ctx context.Context, list []TimelineAsset) ([]TimelineAsset, error) {
	list, err := s.markAdult(ctx, list, hideAdult(ctx))
	if err != nil {
		return nil, err
	}
	if err := s.markCopies(ctx, list); err != nil {
		return nil, err
	}
	return list, s.addMarks(ctx, list)
}

// withHidden is the timeline's filter plus adult items, for a viewer who hides them.
func withHidden(ctx context.Context, f filter) filter {
	if hideAdult(ctx) {
		f.Hide = append(slices.Clone(f.Hide), labels.Adult)
	}
	return f
}

// aiKind is an item's AI label from its source type: only ever set with evidence.
func aiKind(sourceType string) string {
	switch sourceType {
	case "ai_generated":
		return "generated"
	case "ai_edited":
		return "edited"
	}
	return ""
}

type monthInput struct {
	Month       string `query:"month" required:"true" pattern:"^[0-9]{4}-(0[1-9]|1[0-2])$" example:"2026-10"`
	Type        string `query:"type" enum:"image,video" doc:"Only photos (image) or only videos (video)"`
	Collection  string `query:"collection" pattern:"^(favorites|archive|trash|vault|vault-deleted|ai|captured|screenshot|document|receipt|id_card|qr_code|adult|suggestive|(album|person|prompt|model|lora|character|folder|place):[0-9a-f-]{36})$" doc:"Instead of the timeline: favorites, archive, trash, vault, vault-deleted (the vault's trash; both only while it's unlocked), ai, captured, a utility label (screenshot, document, receipt, id_card, qr_code), adult or suggestive, or album, person, prompt, model, lora, character, place or folder:{id} (for a folder, its library's id, with folder=)"`
	Folder      string `query:"folder" maxLength:"2000" doc:"With collection=folder:{library id}: the folder's path in the library (empty for its top)"`
	HideUtility bool   `query:"hideUtility" doc:"Leave screenshots, documents, receipts, ID cards and QR codes out of the timeline"`
	Library     string `query:"library" enum:"mine,household" doc:"Only the viewer's own items (mine), or only the Household's (household); both when left out"`
	MarksFilter
}

type bucketsInput struct {
	Type        string `query:"type" enum:"image,video" doc:"Only photos (image) or only videos (video)"`
	Collection  string `query:"collection" pattern:"^(favorites|archive|trash|vault|vault-deleted|ai|captured|screenshot|document|receipt|id_card|qr_code|adult|suggestive|(album|person|prompt|model|lora|character|folder|place):[0-9a-f-]{36})$" doc:"Instead of the timeline: favorites, archive, trash, vault, vault-deleted (the vault's trash; both only while it's unlocked), ai, captured, a utility label (screenshot, document, receipt, id_card, qr_code), adult or suggestive, or album, person, prompt, model, lora, character, place or folder:{id} (for a folder, its library's id, with folder=)"`
	Folder      string `query:"folder" maxLength:"2000" doc:"With collection=folder:{library id}: the folder's path in the library (empty for its top)"`
	HideUtility bool   `query:"hideUtility" doc:"Leave screenshots, documents, receipts, ID cards and QR codes out of the timeline"`
	Library     string `query:"library" enum:"mine,household" doc:"Only the viewer's own items (mine), or only the Household's (household); both when left out"`
	MarksFilter
}

// hidden lists the labels the timeline leaves out.
func hidden(utility bool) []string {
	if utility {
		return labels.All
	}
	return []string{}
}

// collection splits "album:{id}" into its kind and id; favorites, archive, ai, captured and the
// utility labels have no id.
func collection(c string) (kind string, ref uuid.UUID, err error) {
	kind, id, _ := strings.Cut(c, ":")
	if id != "" {
		if ref, err = uuid.Parse(id); err != nil {
			return "", uuid.Nil, huma.Error422UnprocessableEntity("collection id isn't valid")
		}
	}
	return kind, ref, nil
}

type monthOutput struct {
	Body struct {
		Month  string          `json:"month"`
		Assets []TimelineAsset `json:"assets" doc:"Newest first"`
	}
}

func (s *server) registerTimeline() {
	huma.Register(s.api, huma.Operation{
		OperationID: "timelineBuckets", Method: http.MethodGet, Path: "/api/v1/timeline/buckets", Tags: []string{"Timeline"},
		Summary: "Months in the timeline, with counts", Security: sessionAuth,
	}, func(ctx context.Context, in *bucketsInput) (*bucketsOutput, error) {
		var rows []db.TimelineBucketsRow
		kind, ref, err := collection(in.Collection)
		if err != nil {
			return nil, err
		}
		smart, isSmart := filter{}, false
		if kind == "album" {
			if smart, isSmart, err = s.smartFilter(ctx, ref); err != nil {
				return nil, err
			}
		}
		if in.Collection == "" || isSmart {
			f := timelineFilter(in.Type, in.HideUtility, in.Library, in.MarksFilter)
			if isSmart {
				f = smart.narrow(in.Type, in.MarksFilter)
			}
			f = withHidden(ctx, f)
			if rows, err = s.Store.Q.TimelineBuckets(ctx, f.buckets(viewer(ctx))); err != nil {
				return nil, err
			}
		} else if in.Collection == "trash" {
			trows, err := s.Store.Q.TrashBuckets(ctx, db.TrashBucketsParams{Viewer: viewer(ctx), Type: in.Type})
			if err != nil {
				return nil, err
			}
			for _, r := range trows {
				rows = append(rows, db.TimelineBucketsRow(r))
			}
		} else if kind == "vault" || kind == "vault-deleted" {
			vv, err := unlockedVault(ctx)
			if err != nil {
				return nil, err
			}
			vrows, err := s.Store.Q.VaultBuckets(ctx, db.VaultBucketsParams{VaultViewer: vv, Deleted: kind == "vault-deleted", Type: in.Type})
			if err != nil {
				return nil, err
			}
			for _, r := range vrows {
				rows = append(rows, db.TimelineBucketsRow(r))
			}
		} else {
			prefix, err := folderPrefix(in.Folder)
			if err != nil {
				return nil, err
			}
			crows, err := s.Store.Q.CollectionBuckets(ctx, db.CollectionBucketsParams{Kind: kind, Viewer: viewer(ctx), Ref: ref, Type: in.Type,
				HideAdult: hideAdult(ctx) && kind != labels.Adult,
				MinRating: in.MinRating, Flag: in.Flag, Color: in.Label, Folder: prefix})
			if err != nil {
				return nil, err
			}
			for _, r := range crows {
				rows = append(rows, db.TimelineBucketsRow(r))
			}
		}
		out := &bucketsOutput{}
		out.Body.Buckets = make([]Bucket, len(rows))
		for i, r := range rows {
			out.Body.Buckets[i] = Bucket{Month: r.Month, Count: r.Count}
			out.Body.Total += int64(r.Count)
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "timelineMonth", Method: http.MethodGet, Path: "/api/v1/timeline", Tags: []string{"Timeline"},
		Summary: "Everything in one month of the timeline", Security: sessionAuth,
	}, func(ctx context.Context, in *monthInput) (*monthOutput, error) {
		from, err := time.Parse("2006-01", in.Month)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("month must look like 2026-10")
		}
		var rows []db.TimelineRangeRow
		kind, ref, err := collection(in.Collection)
		if err != nil {
			return nil, err
		}
		smart, isSmart := filter{}, false
		if kind == "album" {
			if smart, isSmart, err = s.smartFilter(ctx, ref); err != nil {
				return nil, err
			}
		}
		if in.Collection == "" || isSmart {
			f := timelineFilter(in.Type, in.HideUtility, in.Library, in.MarksFilter)
			if isSmart {
				f = smart.narrow(in.Type, in.MarksFilter)
			}
			f = withHidden(ctx, f)
			if rows, err = s.Store.Q.TimelineRange(ctx, f.month(viewer(ctx), from)); err != nil {
				return nil, err
			}
		} else if in.Collection == "trash" {
			trows, err := s.Store.Q.TrashRange(ctx, db.TrashRangeParams{Viewer: viewer(ctx), Type: in.Type,
				FromDate: date(from), ToDate: date(from.AddDate(0, 1, 0))})
			if err != nil {
				return nil, err
			}
			for _, r := range trows {
				rows = append(rows, db.TimelineRangeRow(r))
			}
		} else if kind == "vault" || kind == "vault-deleted" {
			vv, err := unlockedVault(ctx)
			if err != nil {
				return nil, err
			}
			vrows, err := s.Store.Q.VaultRange(ctx, db.VaultRangeParams{VaultViewer: vv, Deleted: kind == "vault-deleted", Type: in.Type,
				FromDate: date(from), ToDate: date(from.AddDate(0, 1, 0))})
			if err != nil {
				return nil, err
			}
			for _, r := range vrows {
				rows = append(rows, db.TimelineRangeRow(r))
			}
		} else {
			prefix, err := folderPrefix(in.Folder)
			if err != nil {
				return nil, err
			}
			crows, err := s.Store.Q.CollectionRange(ctx, db.CollectionRangeParams{Kind: kind, Viewer: viewer(ctx), Ref: ref, Type: in.Type,
				HideAdult: hideAdult(ctx) && kind != labels.Adult,
				FromDate:  date(from), ToDate: date(from.AddDate(0, 1, 0)), MinRating: in.MinRating, Flag: in.Flag, Color: in.Label, Folder: prefix})
			if err != nil {
				return nil, err
			}
			for _, r := range crows {
				rows = append(rows, db.TimelineRangeRow(r))
			}
		}
		out := &monthOutput{}
		out.Body.Month = in.Month
		out.Body.Assets = make([]TimelineAsset, len(rows))
		for i, r := range rows {
			out.Body.Assets[i] = TimelineAsset{
				ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: r.LocalDate.Time.Format("2006-01-02"),
				Width: r.Width.Int32, Height: r.Height.Int32, DurationMs: r.DurationMs.Int32,
				ThumbHash: r.Thumbhash, Render: r.RenderState, StackSize: r.StackSize, Favorite: r.Favorite, AI: aiKind(r.SourceType),
				Shared: r.Shared, FromOthers: r.FromOthers,
			}
			out.Body.Assets[i].marks(r.Rating, r.Flag, r.ColorLabel)
			if r.LiveMotionID.Valid {
				id := r.LiveMotionID.UUID
				out.Body.Assets[i].MotionID = &id
			}
		}
		if out.Body.Assets, err = s.markAdult(ctx, out.Body.Assets, false); err != nil { // hidden ones were left out above
			return nil, err
		}
		return out, s.markCopies(ctx, out.Body.Assets)
	})
}

// RecentAsset is an item in Recently added: as the timeline shows it, with when it arrived.
type RecentAsset struct {
	TimelineAsset
	AddedAt time.Time `json:"addedAt" doc:"When it came into Photostead (uploaded, captured, imported, or found by a scan)"`
}

type recentInput struct {
	Type   string `query:"type" enum:"image,video" doc:"Only photos (image) or only videos (video)"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"2000" default:"500" doc:"How many, at most"`
	Cursor string `query:"cursor" maxLength:"100" doc:"From the last page's next: the ones that arrived before"`
}

type recentOutput struct {
	Body struct {
		Assets []RecentAsset `json:"assets" doc:"Newest-arrived first"`
		Next   string        `json:"next,omitempty" doc:"For the next page (cursor=), when there's more"`
	}
}

// recentCursor is where a page of Recently added ends: its last item's arrival time and id.
func recentCursor(at time.Time, id uuid.UUID) string {
	return at.UTC().Format(time.RFC3339Nano) + "_" + id.String()
}

func parseRecentCursor(c string) (*time.Time, uuid.UUID, error) {
	if c == "" {
		return nil, uuid.Nil, nil
	}
	ts, idText, ok := strings.Cut(c, "_")
	at, err := time.Parse(time.RFC3339Nano, ts)
	id, err2 := uuid.Parse(idText)
	if !ok || err != nil || err2 != nil {
		return nil, uuid.Nil, huma.Error422UnprocessableEntity("cursor isn't one this server gave")
	}
	return &at, id, nil
}

func (s *server) registerRecent() {
	huma.Register(s.api, huma.Operation{
		OperationID: "recentlyAdded", Method: http.MethodGet, Path: "/api/v1/timeline/recent", Tags: []string{"Timeline"},
		Summary: "Recently added: what the timeline shows, newest-arrived first, a page at a time", Security: sessionAuth,
	}, func(ctx context.Context, in *recentInput) (*recentOutput, error) {
		before, beforeID, err := parseRecentCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit == 0 {
			limit = 500
		}
		// One more than asked, to know whether there's a next page.
		rows, err := s.Store.Q.RecentlyAdded(ctx, db.RecentlyAddedParams{Viewer: viewer(ctx), Type: in.Type, BeforeAt: before, BeforeID: beforeID, Lim: limit + 1})
		if err != nil {
			return nil, err
		}
		out := &recentOutput{}
		if int32(len(rows)) > limit {
			rows = rows[:limit]
			last := rows[len(rows)-1]
			out.Body.Next = recentCursor(last.AddedAt, last.ID)
		}
		out.Body.Assets = make([]RecentAsset, len(rows))
		for i, r := range rows {
			a := TimelineAsset{
				ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: r.LocalDate.Time.Format("2006-01-02"),
				Width: r.Width.Int32, Height: r.Height.Int32, DurationMs: r.DurationMs.Int32,
				ThumbHash: r.Thumbhash, Render: r.RenderState, StackSize: r.StackSize, Favorite: r.Favorite, AI: aiKind(r.SourceType),
				Shared: r.Shared, FromOthers: r.FromOthers,
			}
			a.marks(r.Rating, r.Flag, r.ColorLabel)
			if r.LiveMotionID.Valid {
				id := r.LiveMotionID.UUID
				a.MotionID = &id
			}
			out.Body.Assets[i] = RecentAsset{TimelineAsset: a, AddedAt: r.AddedAt}
		}
		ids := make([]uuid.UUID, len(rows))
		var videos []uuid.UUID
		for i, r := range rows {
			ids[i] = r.ID
			if r.Type == "video" {
				videos = append(videos, r.ID)
			}
		}
		if len(videos) > 0 {
			copies, err := s.Store.Q.CopiesAmong(ctx, videos)
			if err != nil {
				return nil, err
			}
			has := map[uuid.UUID]bool{}
			for _, id := range copies {
				has[id] = true
			}
			for i := range out.Body.Assets {
				out.Body.Assets[i].Copy = has[out.Body.Assets[i].ID]
			}
		}
		adult, err := s.adultAmong(ctx, ids)
		if err != nil || len(adult) == 0 {
			return out, err
		}
		kept, hide := out.Body.Assets[:0], hideAdult(ctx)
		for _, a := range out.Body.Assets {
			if adult[a.ID] {
				if hide {
					continue // the page may come up short; next still follows on from it
				}
				a.Adult = true
			}
			kept = append(kept, a)
		}
		out.Body.Assets = kept
		return out, nil
	})
}

// Owner is who an item belongs to.
type Owner struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type Asset struct {
	TimelineAsset
	Mime        string         `json:"mime,omitempty"`
	OffsetMin   *int16         `json:"offsetMin,omitempty" doc:"UTC offset recorded with the capture time, in minutes"`
	DateSource  string         `json:"dateSource" doc:"Which metadata the date came from, e.g. exif-original or file-modified"`
	SourceType  string         `json:"sourceType"`
	RenderError string         `json:"renderError,omitempty"`
	FileName    string         `json:"fileName"`
	FolderPath  string         `json:"folderPath" doc:"Folder within its library"`
	Library     string         `json:"library"`
	FileSize    int64          `json:"fileSize"`
	Owner       *Owner         `json:"owner,omitempty" doc:"Whose it is, when it's another member's (shared with the Household)"`
	Camera      *Camera        `json:"camera,omitempty"`
	Location    *Location      `json:"location,omitempty"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Archived    bool           `json:"archived,omitempty"`
	Edited      bool           `json:"edited,omitempty" doc:"It has an edit (see /assets/{id}/edit); the original is as it was"`
	Albums      []Ref          `json:"albums" doc:"The albums it's in"`
	People      []Ref          `json:"people" doc:"People suggested for it, and where the suggestion came from"`
	Characters  []Ref          `json:"characters" doc:"AI characters it shows, and why (metadata, lineage or confirmed)"`
	Generation  *Generation    `json:"generation,omitempty" doc:"How it was made, when the file carries evidence of a generative model"`
	Text        string         `json:"text,omitempty" doc:"Text read from the picture (OCR), one line per line"`
	Labels      []string       `json:"labels" doc:"Utility collections it's in: screenshot, document, receipt, id_card, qr_code, adult, suggestive"`
	Video       *VideoPlayback `json:"video,omitempty" doc:"For a video, once probed: what to play"`
	Keywords    []string       `json:"keywords" doc:"Its keywords (tags), e.g. brought over by an import"`
	Trashed     bool           `json:"trashed,omitempty" doc:"In the trash: deleted for good 30 days after it was trashed"`
	TrashedAt   *time.Time     `json:"trashedAt,omitempty"`
}

// Generation is what an item's own metadata says about how it was generated.
type Generation struct {
	Kind        string         `json:"kind" enum:"generated,edited"`
	Source      string         `json:"source" enum:"comfyui,a1111,midjourney,grok,aigc,c2pa,iptc" doc:"The kind of evidence"`
	Evidence    string         `json:"evidence" doc:"Where it was found, e.g. png:prompt, exif:usercomment, video:comment, c2pa"`
	Tool        string         `json:"tool,omitempty"`
	ToolVersion string         `json:"toolVersion,omitempty"`
	Model       string         `json:"model,omitempty"`
	ModelID     *uuid.UUID     `json:"modelId,omitempty"`
	Loras       []LoraUse      `json:"loras"`
	Prompt      string         `json:"prompt,omitempty"`
	PromptID    *uuid.UUID     `json:"promptId,omitempty"`
	PromptUses  int32          `json:"promptUses,omitempty" doc:"How many items in the library share this prompt"`
	Negative    string         `json:"negative,omitempty"`
	Seed        string         `json:"seed,omitempty"`
	Sampler     string         `json:"sampler,omitempty"`
	Scheduler   string         `json:"scheduler,omitempty"`
	Steps       int32          `json:"steps,omitempty"`
	CFG         float32        `json:"cfg,omitempty"`
	Width       int32          `json:"width,omitempty"`
	Height      int32          `json:"height,omitempty"`
	Settings    map[string]any `json:"settings,omitempty" doc:"Everything else that was read"`
	HasWorkflow bool           `json:"hasWorkflow" doc:"A ComfyUI workflow can be downloaded"`
	Incomplete  bool           `json:"incomplete,omitempty" doc:"Some details are still to be read from the file"`
	From        *Sample        `json:"from,omitempty" doc:"For an image-to-video clip, the image it was made from"`
}

type LoraUse struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Weight *float32  `json:"weight,omitempty"`
}

// Ref names something an item belongs to.
type Ref struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Source string    `json:"source,omitempty" doc:"Why: faces (recognized), immich (a suggestion), or for a character metadata, lineage, faces or confirmed"`
	Kind   string    `json:"kind,omitempty" doc:"For people: human or pet"`
}

type Camera struct {
	Make      string   `json:"make,omitempty"`
	Model     string   `json:"model,omitempty"`
	Lens      string   `json:"lens,omitempty"`
	FocalMM   *float32 `json:"focalMm,omitempty"`
	FNumber   *float32 `json:"fNumber,omitempty"`
	ExposureS *float32 `json:"exposureS,omitempty"`
	ISO       *int32   `json:"iso,omitempty"`
}

type Location struct {
	Lat     float64    `json:"lat"`
	Lon     float64    `json:"lon"`
	Place   string     `json:"place,omitempty" doc:"The nearest town, its region and country (named offline)"`
	PlaceID *uuid.UUID `json:"placeId,omitempty" doc:"List the place's items with collection=place:{placeId}"`
}

type assetInput struct {
	ID uuid.UUID `path:"id"`
}

type assetOutput struct{ Body Asset }

type originalInput struct {
	ID       uuid.UUID `path:"id"`
	Download bool      `query:"download" doc:"Save it (as an attachment, with its file name) rather than show it"`
}

func (s *server) registerAssets() {
	huma.Register(s.api, huma.Operation{
		OperationID: "getAsset", Method: http.MethodGet, Path: "/api/v1/assets/{id}", Tags: []string{"Assets"},
		Summary: "One photo or video, with its details", Security: sessionAuth,
	}, func(ctx context.Context, in *assetInput) (*assetOutput, error) {
		r, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: viewer(ctx), VaultViewer: vaultViewer(ctx), ID: in.ID})
		if err != nil {
			return nil, notFound(err)
		}
		a := Asset{
			TimelineAsset: TimelineAsset{ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: r.LocalDate.Time.Format("2006-01-02"),
				Width: r.Width.Int32, Height: r.Height.Int32, DurationMs: r.DurationMs.Int32, ThumbHash: r.Thumbhash, Render: r.RenderState,
				Favorite: r.Favorite, Shared: r.Shared, FromOthers: r.OwnerID != viewer(ctx)},
			Mime: r.Mime.String, DateSource: r.DateSource, SourceType: r.SourceType, RenderError: r.RenderError.String,
			FileName: filepath.Base(r.RelPath.String), FolderPath: filepath.Dir(r.RelPath.String), Library: r.LibraryName.String,
			FileSize: r.FileSize.Int64, Title: r.Title.String, Description: r.Description.String,
			Archived: r.Visibility == "archived", Albums: []Ref{}, People: []Ref{}, Characters: []Ref{},
		}
		if marks, err := s.Store.Q.AssetMarks(ctx, db.AssetMarksParams{Viewer: viewer(ctx), Ids: []uuid.UUID{r.ID}}); err != nil {
			return nil, err
		} else if len(marks) == 1 {
			a.marks(marks[0].Rating, marks[0].Flag, marks[0].ColorLabel)
		}
		if r.TrashedAt != nil {
			a.Trashed, a.TrashedAt = true, r.TrashedAt
		}
		if _, err := s.Store.Q.AssetEdit(ctx, r.ID); err == nil {
			a.Edited = true
		}
		albums, err := s.Store.Q.AssetAlbums(ctx, db.AssetAlbumsParams{AssetID: r.ID, Viewer: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		for _, al := range albums {
			a.Albums = append(a.Albums, Ref{ID: al.ID, Name: al.Title})
		}
		people, err := s.Store.Q.AssetPeople(ctx, db.AssetPeopleParams{AssetID: r.ID, Viewer: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		for _, p := range people {
			a.People = append(a.People, Ref{ID: p.ID, Name: p.Name, Source: p.Source, Kind: p.Kind})
		}
		chars, err := s.Store.Q.AssetCharacters(ctx, db.AssetCharactersParams{AssetID: r.ID, Viewer: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		for _, c := range chars {
			a.Characters = append(a.Characters, Ref{ID: c.ID, Name: c.Name, Source: c.Basis})
		}
		if t, err := s.Store.Q.AssetText(ctx, r.ID); err == nil {
			a.Text = t
		}
		written := false
		if d, err := s.Store.Q.AssetDescription(ctx, r.ID); err == nil && (d.Text != "" || d.Source == "user") {
			a.Description = d.Text // written or brought over here: it wins over the file's own (written here, even empty)
			written = d.Source == "user"
		}
		if a.Keywords, err = s.Store.Q.AssetKeywords(ctx, r.ID); err != nil {
			return nil, err
		}
		if a.Keywords == nil {
			a.Keywords = []string{}
		}
		ls, err := s.Store.Q.AssetLabels(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		a.Labels = []string{}
		for _, l := range ls {
			a.Labels = append(a.Labels, l.Label)
			a.Adult = a.Adult || l.Label == labels.Adult
		}
		if r.Type == "video" {
			if v, err := s.Store.Q.VideoPlayback(ctx, r.ID); err == nil {
				a.Video = &VideoPlayback{Type: v.Codecs, Codec: v.Codec, HDR: v.Hdr, Direct: v.Direct, Copy: v.HasCopy, Waiting: v.Waiting}
				a.Copy = v.HasCopy
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		}
		if a.Generation, err = s.generation(ctx, r.ID); err != nil {
			return nil, err
		}
		if a.Generation != nil {
			a.AI = a.Generation.Kind
			if !written && provenance.Repeats(a.Description, a.Generation.Prompt) {
				a.Description = "" // it's the prompt, shown as one
			}
		}
		if r.TakenOffsetMin.Valid {
			a.OffsetMin = &r.TakenOffsetMin.Int16
		}
		if r.Make.Valid || r.Model.Valid || r.Lens.Valid {
			a.Camera = &Camera{Make: r.Make.String, Model: r.Model.String, Lens: r.Lens.String,
				FocalMM: f4(r.FocalMm), FNumber: f4(r.Fnumber), ExposureS: f4(r.ExposureS), ISO: i4(r.Iso)}
		}
		if r.Lat.Valid && r.Lon.Valid {
			a.Location = &Location{Lat: r.Lat.Float64, Lon: r.Lon.Float64}
			if p, err := s.Store.Q.AssetPlace(ctx, r.ID); err == nil {
				a.Location.Place, a.Location.PlaceID = placeName(p.Name, p.Admin1, p.Country), &p.ID
			}
		}
		if r.OwnerID != viewer(ctx) {
			a.Owner = &Owner{ID: r.OwnerID, Name: r.OwnerName}
		}
		return &assetOutput{Body: a}, nil
	})

	for _, size := range []string{"thumbnail", "preview"} {
		size := size
		huma.Register(s.api, huma.Operation{
			OperationID: "get" + upper(size), Method: http.MethodGet, Path: "/api/v1/assets/{id}/" + size, Tags: []string{"Assets"},
			Summary:  map[string]string{"thumbnail": "Thumbnail (WebP, about 640px)", "preview": "Preview (WebP, about 1600px)"}[size],
			Security: sessionAuth,
			Responses: map[string]*huma.Response{"200": {Description: "WebP image",
				Content: map[string]*huma.MediaType{"image/webp": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
		}, func(ctx context.Context, in *assetInput) (*huma.StreamResponse, error) {
			a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: viewer(ctx), VaultViewer: vaultViewer(ctx), ID: in.ID})
			if err != nil {
				return nil, notFound(err)
			}
			thumb, preview := s.Render.Paths(in.ID)
			p := map[string]string{"thumbnail": thumb, "preview": preview}[size]
			if _, err := os.Stat(p); err != nil {
				return nil, huma.Error404NotFound("Not rendered yet.")
			}
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				r, w := humago.Unwrap(hctx)
				w.Header().Set("Content-Type", "image/webp")
				w.Header().Set("Cache-Control", cacheFor(a.Visibility, "private, max-age=86400"))
				http.ServeFile(w, r, p)
			}}, nil
		})
	}

	huma.Register(s.api, huma.Operation{
		OperationID: "getOriginal", Method: http.MethodGet, Path: "/api/v1/assets/{id}/original", Tags: []string{"Assets"},
		Summary: "The original file, byte for byte", Description: "Supports Range requests, so videos can be played and seeked.",
		Security: sessionAuth,
		Responses: map[string]*huma.Response{"200": {Description: "The original file",
			Content: map[string]*huma.MediaType{"application/octet-stream": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}},
	}, func(ctx context.Context, in *originalInput) (*huma.StreamResponse, error) {
		a, err := s.Store.Q.GetVisibleAsset(ctx, db.GetVisibleAssetParams{Viewer: viewer(ctx), VaultViewer: vaultViewer(ctx), ID: in.ID})
		if err != nil {
			return nil, notFound(err)
		}
		if !a.RootPath.Valid || !a.RelPath.Valid {
			return nil, errNotFound
		}
		path := filepath.Join(a.RootPath.String, filepath.FromSlash(a.RelPath.String))
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			return nil, huma.Error404NotFound("The original file isn't reachable right now.")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			defer f.Close()
			r, w := humago.Unwrap(hctx)
			info, err := f.Stat()
			if err != nil {
				http.Error(w, "unreadable", http.StatusInternalServerError)
				return
			}
			ct := a.Mime.String
			if ct == "" {
				ct = mime.TypeByExtension(filepath.Ext(path))
			}
			if ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Cache-Control", cacheFor(a.Visibility, "private, max-age=3600"))
			if in.Download {
				w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
			} else {
				w.Header().Set("Content-Disposition", "inline")
			}
			http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
		}}, nil
	})
}

func date(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

func f4(v pgtype.Float4) *float32 {
	if !v.Valid {
		return nil
	}
	return &v.Float32
}

func i4(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return &v.Int32
}

func upper(s string) string { return string(s[0]-32) + s[1:] }

func (s *server) generation(ctx context.Context, id uuid.UUID) (*Generation, error) {
	g, err := s.Store.Q.GenerationForAsset(ctx, db.GenerationForAssetParams{Viewer: viewer(ctx), ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	out := &Generation{Kind: g.Kind, Source: g.Source, Evidence: g.Evidence, Tool: g.Tool.String, ToolVersion: g.ToolVersion.String,
		Model: g.Model.String, Prompt: g.Prompt.String, Negative: g.Negative.String, Seed: g.Seed.String,
		Sampler: g.Sampler.String, Scheduler: g.Scheduler.String, Steps: g.Steps.Int32, CFG: g.Cfg.Float32,
		Width: g.GenWidth.Int32, Height: g.GenHeight.Int32, HasWorkflow: g.HasWorkflow, Incomplete: g.NeedsFile, Loras: []LoraUse{}}
	if g.Prompt.Valid {
		out.PromptUses = g.PromptUses
	}
	if g.ModelID.Valid {
		out.ModelID = &g.ModelID.UUID
	}
	if g.PromptID.Valid {
		out.PromptID = &g.PromptID.UUID
	}
	if len(g.Settings) > 2 {
		_ = json.Unmarshal(g.Settings, &out.Settings)
	}
	loras, err := s.Store.Q.GenerationLoras(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, l := range loras {
		u := LoraUse{ID: l.ID, Name: l.Display}
		if l.Weight.Valid {
			w := l.Weight.Float32
			u.Weight = &w
		}
		out.Loras = append(out.Loras, u)
	}
	from, err := s.Store.Q.GenerationStart(ctx, db.GenerationStartParams{Viewer: viewer(ctx), ID: id})
	if err == nil {
		out.From = &Sample{ID: from.ID, ThumbHash: from.Thumbhash}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return out, nil
}

func (s *server) registerGeneration() {
	huma.Register(s.api, huma.Operation{
		OperationID: "getWorkflow", Method: http.MethodGet, Path: "/api/v1/assets/{id}/workflow", Tags: []string{"Assets"},
		Summary:     "The ComfyUI workflow an item was made with",
		Description: "Exactly as found in the file: the editor workflow, or the API graph when that's all there is. Drop it on ComfyUI to load it.",
		Security:    sessionAuth,
		Responses: map[string]*huma.Response{"200": {Description: "Workflow JSON",
			Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: "object"}}}}},
	}, func(ctx context.Context, in *assetInput) (*huma.StreamResponse, error) {
		w, err := s.Store.Q.GenerationWorkflow(ctx, db.GenerationWorkflowParams{Viewer: viewer(ctx), ID: in.ID})
		if err != nil {
			return nil, notFound(err)
		}
		name := strings.TrimSuffix(filepath.Base(w.RelPath.String), filepath.Ext(w.RelPath.String))
		if name == "" || name == "." {
			name = "workflow"
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", "application/json")
			hctx.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + "-workflow.json"}))
			hctx.SetHeader("Cache-Control", "private, no-store")
			_, _ = hctx.BodyWriter().Write([]byte(w.Json))
		}}, nil
	})
}

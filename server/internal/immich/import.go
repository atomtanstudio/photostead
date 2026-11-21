package immich

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

const source = "immich"

// Importer brings an Immich user's favorites, archive, albums, people's names, descriptions and
// tags over to their Photostead library. Running it again brings over what changed in Immich since, and keeps
// what was changed here.
type Importer struct {
	Store  *store.Store
	Client *Client
	Log    *slog.Logger
	DryRun bool              // work everything out, change nothing
	Step   func(step string) // progress, for the people watching
}

// Summary is what an import found and did. With DryRun, the counts are what it would do.
type Summary struct {
	ImmichVersion string `json:"immichVersion"`
	DryRun        bool   `json:"dryRun,omitempty"`

	Items             int      `json:"items"`     // Immich photos and videos read (not trashed, not Live Photo clips)
	Matched           int      `json:"matched"`   // found here
	ByPath            int      `json:"byPath"`    // found by where the file is
	ByContent         int      `json:"byContent"` // found by the file's bytes
	Unmatched         int      `json:"unmatched"` // not in Photostead (usually excluded folders)
	UnmatchedExamples []string `json:"unmatchedExamples,omitempty"`
	Folders           []string `json:"folders"` // "Immich folder → Photostead library", as learned

	Favorites       int `json:"favorites"` // favorites in Immich, found here
	FavoritesSet    int `json:"favoritesSet"`
	FavoritesOff    int `json:"favoritesOff"`
	Archived        int `json:"archived"`
	ArchivedSet     int `json:"archivedSet"`
	ArchivedOff     int `json:"archivedOff"`
	Albums          int `json:"albums"`
	AlbumsNew       int `json:"albumsNew"`
	AlbumItems      int `json:"albumItems"`    // album entries found here
	AlbumsMissing   int `json:"albumsMissing"` // album entries not in Photostead
	People          int `json:"people"`        // named people
	PeopleNew       int `json:"peopleNew"`
	PeopleItems     int `json:"peopleItems"`     // suggestions found here
	Ratings         int `json:"ratings"`         // items here rated (or rejected) in Immich
	RatingsSet      int `json:"ratingsSet"`      // brought over (items not rated here yet)
	Descriptions    int `json:"descriptions"`    // items here with a description in Immich
	DescriptionsSet int `json:"descriptionsSet"` // brought over (new or changed)
	Tags            int `json:"tags"`            // Immich tags
	TagItems        int `json:"tagItems"`        // tag uses found here

	Skipped []string `json:"skipped,omitempty"` // parts that couldn't be read, and why
}

func (im *Importer) step(s string) {
	if im.Step != nil {
		im.Step(s)
	}
	if im.Log != nil {
		im.Log.Info("immich import: " + s)
	}
}

// Run imports for one owner.
func (im *Importer) Run(ctx context.Context, owner uuid.UUID) (Summary, error) {
	q := im.Store.Q
	sum := Summary{DryRun: im.DryRun}
	v, err := im.Client.Version(ctx)
	if err != nil {
		return sum, fmt.Errorf("couldn't reach Immich: %w", err)
	}
	sum.ImmichVersion = v

	im.step("Reading Photostead's files")
	rows, err := q.ImportFiles(ctx, owner)
	if err != nil {
		return sum, err
	}
	files := make([]File, len(rows))
	for i, r := range rows {
		files[i] = File{Library: r.LibraryID, RelPath: r.RelPath, SHA1: r.Sha1, Asset: r.AssetID}
	}
	m := newMatcher(files)

	im.step("Reading Immich's items")
	var assets []Asset
	seen := map[string]bool{}
	for _, vis := range []string{"timeline", "archive"} {
		err := im.Client.Search(ctx, map[string]any{"visibility": vis, "withExif": true}, func(page []Asset) error {
			for _, a := range page {
				if !seen[a.ID] && usable(a) {
					seen[a.ID] = true
					assets = append(assets, a)
				}
			}
			im.step(fmt.Sprintf("Reading Immich's items · %d", len(assets)))
			return ctx.Err()
		})
		if err != nil {
			return sum, fmt.Errorf("reading Immich's items: %w", err)
		}
	}
	sum.Items = len(assets)

	im.step("Matching items")
	m.learn(assets)
	libs, err := q.ListLibraries(ctx)
	if err != nil {
		return sum, err
	}
	libName := map[uuid.UUID]string{}
	for _, l := range libs {
		libName[l.ID] = l.Name
	}
	for _, f := range m.folders {
		sum.Folders = append(sum.Folders, fmt.Sprintf("%s → %s (%d)", f.Prefix, libName[f.Library], f.Votes))
	}
	target := map[string]uuid.UUID{} // Immich id -> item here
	type state struct{ favorite, archived bool }
	states := map[uuid.UUID]*state{}
	descriptions := map[uuid.UUID]string{} // the longest, when several Immich items are one here
	ratings := map[uuid.UUID]int{}         // stars, or -1 for a reject
	for _, a := range assets {
		id, how, ok := m.match(a)
		if !ok {
			sum.Unmatched++
			if len(sum.UnmatchedExamples) < 12 {
				sum.UnmatchedExamples = append(sum.UnmatchedExamples, a.OriginalPath)
			}
			continue
		}
		sum.Matched++
		if how == "path" {
			sum.ByPath++
		} else {
			sum.ByContent++
		}
		target[a.ID] = id
		if r := a.Rating(); r != 0 && (ratings[id] == 0 || r > ratings[id]) {
			ratings[id] = r // copies rated differently: the most stars
		}
		if d := a.Description(); len(d) > len(descriptions[id]) {
			descriptions[id] = d
		}
		// One item here can be several in Immich (the same bytes in two places): it's a favorite
		// if any of them is, and archived only if all of them are.
		archived := a.Visibility == "archive"
		if s := states[id]; s == nil {
			states[id] = &state{favorite: a.IsFavorite, archived: archived}
		} else {
			s.favorite = s.favorite || a.IsFavorite
			s.archived = s.archived && archived
		}
	}

	im.step("Bringing over favorites and the archive")
	prev := map[uuid.UUID]state{}
	prevRows, err := q.ImportAssetStates(ctx, db.ImportAssetStatesParams{OwnerID: owner, Source: source})
	if err != nil {
		return sum, err
	}
	for _, r := range prevRows {
		prev[r.AssetID] = state{r.Favorite, r.Archived}
	}
	var favOn, favOff, archOn, archOff, ids []uuid.UUID
	var favs, archs []bool
	for id, s := range states {
		p := prev[id] // nothing imported before reads as neither
		if s.favorite {
			sum.Favorites++
		}
		if s.archived {
			sum.Archived++
		}
		switch {
		case s.favorite && !p.favorite:
			favOn = append(favOn, id)
		case !s.favorite && p.favorite:
			favOff = append(favOff, id)
		}
		switch {
		case s.archived && !p.archived:
			archOn = append(archOn, id)
		case !s.archived && p.archived:
			archOff = append(archOff, id)
		}
		ids, favs, archs = append(ids, id), append(favs, s.favorite), append(archs, s.archived)
	}
	sum.FavoritesSet, sum.FavoritesOff = len(favOn), len(favOff)
	sum.ArchivedSet, sum.ArchivedOff = len(archOn), len(archOff)
	if !im.DryRun {
		for _, c := range []struct {
			ids []uuid.UUID
			on  bool
		}{{favOn, true}, {favOff, false}} {
			for _, part := range chunks(c.ids) {
				if err := q.SetFavorite(ctx, db.SetFavoriteParams{AssetIds: part, UserID: owner, Favorite: c.on}); err != nil {
					return sum, err
				}
			}
		}
		for _, c := range []struct {
			ids []uuid.UUID
			on  bool
		}{{archOn, true}, {archOff, false}} {
			for _, part := range chunks(c.ids) {
				if _, err := q.SetArchived(ctx, db.SetArchivedParams{Archived: c.on, OwnerID: owner, AssetIds: part}); err != nil {
					return sum, err
				}
			}
		}
		for i := 0; i < len(ids); i += chunkSize {
			j := min(i+chunkSize, len(ids))
			if err := q.SaveImportAssetStates(ctx, db.SaveImportAssetStatesParams{OwnerID: owner, Source: source,
				AssetIds: ids[i:j], Favorites: favs[i:j], Archived: archs[i:j]}); err != nil {
				return sum, err
			}
		}
		if err := saveMap(ctx, q, owner, "asset", target); err != nil {
			return sum, err
		}
	}

	im.step("Bringing over ratings")
	sum.Ratings = len(ratings)
	var rIDs []uuid.UUID
	var rs []int16
	for id, r := range ratings {
		rIDs, rs = append(rIDs, id), append(rs, int16(r))
	}
	if !im.DryRun {
		for i := 0; i < len(rIDs); i += chunkSize {
			j := min(i+chunkSize, len(rIDs))
			n, err := q.ImportRatings(ctx, db.ImportRatingsParams{OwnerID: owner, AssetIds: rIDs[i:j], Ratings: rs[i:j]})
			if err != nil {
				return sum, err
			}
			sum.RatingsSet += int(n)
		}
	}

	im.step("Bringing over descriptions")
	sum.Descriptions = len(descriptions)
	var dIDs []uuid.UUID
	var dTexts []string
	for id, d := range descriptions {
		dIDs, dTexts = append(dIDs, id), append(dTexts, d)
	}
	if !im.DryRun {
		for i := 0; i < len(dIDs); i += chunkSize {
			j := min(i+chunkSize, len(dIDs))
			n, err := q.ImportDescriptions(ctx, db.ImportDescriptionsParams{AssetIds: dIDs[i:j], Texts: dTexts[i:j], Source: source})
			if err != nil {
				return sum, err
			}
			sum.DescriptionsSet += int(n)
		}
	}

	if err := im.albums(ctx, owner, m, target, &sum); err != nil {
		return sum, err
	}
	if err := im.tags(ctx, owner, m, &sum); err != nil {
		return sum, err
	}
	if err := im.people(ctx, owner, m, &sum); err != nil {
		return sum, err
	}
	return sum, nil
}

// tags brings tags over as keywords, tag by tag. Listing tags needs the tag.read permission;
// without it the import goes on and says so.
func (im *Importer) tags(ctx context.Context, owner uuid.UUID, m *matcher, sum *Summary) error {
	im.step("Bringing over tags")
	tags, err := im.Client.Tags(ctx)
	if p := MissingPermission(err); p != "" {
		sum.Skipped = append(sum.Skipped, "tags (the key needs the "+p+" permission)")
		return nil
	} else if err != nil {
		return fmt.Errorf("reading Immich's tags: %w", err)
	}
	sum.Tags = len(tags)
	var aIDs, kIDs []uuid.UUID
	for i, t := range tags {
		name := strings.TrimSpace(t.Value)
		if name == "" {
			name = strings.TrimSpace(t.Name)
		}
		if name == "" {
			continue
		}
		found, _, err := im.members(ctx, m, map[string]any{"tagIds": []string{t.ID}})
		if err != nil {
			return fmt.Errorf("reading tag %q: %w", name, err)
		}
		if i%100 == 0 {
			im.step(fmt.Sprintf("Bringing over tags · %d of %d", i, len(tags)))
		}
		if len(found) == 0 {
			continue
		}
		sum.TagItems += len(found)
		if im.DryRun {
			continue
		}
		rows, err := im.Store.Q.EnsureKeywords(ctx, db.EnsureKeywordsParams{OwnerID: owner, Names: []string{name}})
		if err != nil || len(rows) == 0 {
			return err
		}
		for _, id := range found {
			aIDs, kIDs = append(aIDs, id), append(kIDs, rows[0].ID)
		}
	}
	for i := 0; i < len(aIDs); i += chunkSize {
		j := min(i+chunkSize, len(aIDs))
		if _, err := im.Store.Q.AddAssetKeywords(ctx, db.AddAssetKeywordsParams{AssetIds: aIDs[i:j], KeywordIds: kIDs[i:j], Source: source}); err != nil {
			return err
		}
	}
	return nil
}

// usable says whether an Immich item is one the import considers: photos and videos that
// aren't in the trash, Live Photo clips (Immich hides those) or the locked folder.
func usable(a Asset) bool {
	return (a.Type == "IMAGE" || a.Type == "VIDEO") && !a.IsTrashed && (a.Visibility == "timeline" || a.Visibility == "archive")
}

// members reads the items of an Immich album or person, archived ones included, and finds
// them here.
func (im *Importer) members(ctx context.Context, m *matcher, filter map[string]any) (found []uuid.UUID, missing int, err error) {
	seen := map[uuid.UUID]bool{}
	for _, vis := range []string{"timeline", "archive"} {
		f := map[string]any{"visibility": vis}
		for k, v := range filter {
			f[k] = v
		}
		err = im.Client.Search(ctx, f, func(page []Asset) error {
			for _, a := range page {
				if !usable(a) {
					continue
				}
				id, _, ok := m.match(a)
				if !ok {
					missing++
				} else if !seen[id] {
					seen[id] = true
					found = append(found, id)
				}
			}
			return ctx.Err()
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return found, missing, nil
}

func (im *Importer) albums(ctx context.Context, owner uuid.UUID, m *matcher, target map[string]uuid.UUID, sum *Summary) error {
	q := im.Store.Q
	im.step("Reading albums")
	albums, err := im.Client.Albums(ctx)
	if p := MissingPermission(err); p != "" {
		sum.Skipped = append(sum.Skipped, "Albums weren't imported: the key needs the "+p+" permission")
		return nil
	} else if err != nil {
		return fmt.Errorf("reading albums: %w", err)
	}
	known, err := existing(ctx, q, owner, "album")
	if err != nil {
		return err
	}
	made := map[string]uuid.UUID{}
	for i, al := range albums {
		im.step(fmt.Sprintf("Albums · %d of %d", i+1, len(albums)))
		found, missing, err := im.members(ctx, m, map[string]any{"albumIds": []string{al.ID}})
		if err != nil {
			return fmt.Errorf("reading album %q: %w", al.Name, err)
		}
		sum.Albums++
		sum.AlbumItems += len(found)
		sum.AlbumsMissing += missing
		if im.DryRun {
			if _, ok := known[al.ID]; !ok {
				sum.AlbumsNew++
			}
			continue
		}
		cover := uuid.NullUUID{}
		if id, ok := target[al.CoverID]; ok {
			cover = uuid.NullUUID{UUID: id, Valid: true}
		}
		id, ok := known[al.ID]
		if ok {
			n, err := q.UpdateImportedAlbum(ctx, db.UpdateImportedAlbumParams{Title: al.Name, Description: al.Description,
				CoverAssetID: cover, ID: id, OwnerID: owner})
			if err != nil {
				return err
			}
			ok = n == 1
		}
		if !ok { // new, or deleted here since: make it
			created := al.CreatedAt
			if created.IsZero() {
				created = time.Now()
			}
			id, err = q.CreateAlbum(ctx, db.CreateAlbumParams{OwnerID: owner, Title: al.Name, Description: al.Description,
				CoverAssetID: cover, CreatedAt: created})
			if err != nil {
				return err
			}
			sum.AlbumsNew++
		}
		made[al.ID] = id
		via := pgtype.Text{String: source, Valid: true}
		if _, err := q.AddAlbumAssets(ctx, db.AddAlbumAssetsParams{AlbumID: id, AssetIds: found,
			UserID: uuid.NullUUID{UUID: owner, Valid: true}, Via: via}); err != nil {
			return err
		}
		if _, err := q.RemoveImportedAlbumAssets(ctx, db.RemoveImportedAlbumAssetsParams{AlbumID: id, Via: via, AssetIds: found}); err != nil {
			return err
		}
	}
	if im.DryRun {
		return nil
	}
	return saveMap(ctx, q, owner, "album", made)
}

func (im *Importer) people(ctx context.Context, owner uuid.UUID, m *matcher, sum *Summary) error {
	q := im.Store.Q
	im.step("Reading people")
	all, err := im.Client.People(ctx)
	if p := MissingPermission(err); p != "" {
		sum.Skipped = append(sum.Skipped, "People weren't imported: the key needs the "+p+" permission")
		return nil
	} else if err != nil {
		return fmt.Errorf("reading people: %w", err)
	}
	known, err := existing(ctx, q, owner, "person")
	if err != nil {
		return err
	}
	made := map[string]uuid.UUID{}
	// Only named people: unnamed clusters are left to Photostead's own face recognition.
	for _, p := range all {
		if p.Name == "" {
			continue
		}
		im.step("People · " + p.Name)
		found, _, err := im.members(ctx, m, map[string]any{"personIds": []string{p.ID}})
		if err != nil {
			return fmt.Errorf("reading %s's items: %w", p.Name, err)
		}
		sum.People++
		sum.PeopleItems += len(found)
		if im.DryRun {
			if _, ok := known[p.ID]; !ok {
				sum.PeopleNew++
			}
			continue
		}
		birth := pgtype.Date{}
		if t, err := time.Parse("2006-01-02", p.BirthDate); err == nil {
			birth = pgtype.Date{Time: t, Valid: true}
		}
		id, ok := known[p.ID]
		if ok {
			n, err := q.UpdateImportedPerson(ctx, db.UpdateImportedPersonParams{Name: p.Name, BirthDate: birth, Hidden: p.IsHidden,
				Favorite: p.IsFavorite, ID: id, OwnerID: owner})
			if err != nil {
				return err
			}
			ok = n == 1
		}
		if !ok {
			id, err = q.CreatePerson(ctx, db.CreatePersonParams{OwnerID: owner, Name: p.Name, BirthDate: birth, Hidden: p.IsHidden, Favorite: p.IsFavorite})
			if err != nil {
				return err
			}
			sum.PeopleNew++
		}
		made[p.ID] = id
		if _, err := q.AddPersonSuggestions(ctx, db.AddPersonSuggestionsParams{PersonID: id, AssetIds: found, Source: source}); err != nil {
			return err
		}
		if _, err := q.RemovePersonSuggestions(ctx, db.RemovePersonSuggestionsParams{PersonID: id, Source: source, AssetIds: found}); err != nil {
			return err
		}
	}
	if im.DryRun {
		return nil
	}
	return saveMap(ctx, q, owner, "person", made)
}

func existing(ctx context.Context, q *db.Queries, owner uuid.UUID, kind string) (map[string]uuid.UUID, error) {
	rows, err := q.ImportMap(ctx, db.ImportMapParams{OwnerID: owner, Source: source, Kind: kind})
	if err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(rows))
	for _, r := range rows {
		out[r.SourceID] = r.TargetID
	}
	return out, nil
}

func saveMap(ctx context.Context, q *db.Queries, owner uuid.UUID, kind string, m map[string]uuid.UUID) error {
	src := make([]string, 0, len(m))
	dst := make([]uuid.UUID, 0, len(m))
	for k, v := range m {
		src, dst = append(src, k), append(dst, v)
	}
	for i := 0; i < len(src); i += chunkSize {
		j := min(i+chunkSize, len(src))
		if err := q.SaveImportMap(ctx, db.SaveImportMapParams{OwnerID: owner, Source: source, Kind: kind,
			SourceIds: src[i:j], TargetIds: dst[i:j]}); err != nil {
			return err
		}
	}
	return nil
}

const chunkSize = 5000

func chunks(ids []uuid.UUID) [][]uuid.UUID {
	var out [][]uuid.UUID
	for i := 0; i < len(ids); i += chunkSize {
		out = append(out, ids[i:min(i+chunkSize, len(ids))])
	}
	return out
}

// Start records an import as asked for, so the app can show it waiting before it begins.
func Start(ctx context.Context, q *db.Queries, owner uuid.UUID) (uuid.UUID, error) {
	return q.StartImport(ctx, db.StartImportParams{OwnerID: owner, Source: source, Step: pgtype.Text{String: "Waiting to start", Valid: true}})
}

// Record runs an import and keeps a record of it (its step while running, and its summary or
// error when done), which the app shows. runID is the record Start made, or uuid.Nil to make
// one now.
func Record(ctx context.Context, im *Importer, owner, runID uuid.UUID) (Summary, error) {
	q := im.Store.Q
	if runID == uuid.Nil {
		var err error
		if runID, err = Start(ctx, q, owner); err != nil {
			return Summary{}, err
		}
	}
	if err := q.InterruptedImports(ctx, db.InterruptedImportsParams{OwnerID: owner, Source: source, Keep: runID}); err != nil {
		return Summary{}, err
	}
	if err := q.ImportStep(ctx, db.ImportStepParams{ID: runID, Step: pgtype.Text{String: "Starting", Valid: true}}); err != nil {
		return Summary{}, err
	}
	last := time.Time{}
	im.Step = func(s string) {
		if time.Since(last) < time.Second { // progress is for people; a second is plenty
			return
		}
		last = time.Now()
		_ = q.ImportStep(ctx, db.ImportStepParams{ID: runID, Step: pgtype.Text{String: s, Valid: true}})
	}
	sum, runErr := im.Run(ctx, owner)
	body, _ := json.Marshal(sum)
	fin := db.FinishImportParams{ID: runID, State: "done", Summary: body}
	if runErr != nil {
		fin.State = "failed"
		fin.Error = pgtype.Text{String: runErr.Error(), Valid: true}
	}
	// Record the outcome even when the run was cancelled.
	if err := q.FinishImport(context.WithoutCancel(ctx), fin); err != nil && runErr == nil {
		return sum, err
	}
	return sum, runErr
}

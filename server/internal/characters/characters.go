// Package characters works out which AI items show which character (decision 5.6): from the
// character's LoRAs, and for videos from the images they were made from (lineage). Face
// similarity never makes anyone a member; an owner's own confirmations and rejections always win.
package characters

import (
	"context"
	"fmt"
	"math"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/provenance"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// Compile turns a character's LoRA patterns into matchers: case-insensitive regular expressions
// (RE2, so a pattern can't take forever), matched against the catalog's normalized LoRA names.
func Compile(patterns []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// Exactly is the pattern for one LoRA by name.
func Exactly(lora string) string { return "^" + regexp.QuoteMeta(provenance.Normalize(lora)) + "$" }

// Matches says whether a LoRA name matches any of the patterns.
func Matches(res []*regexp.Regexp, name string) bool {
	name = provenance.Normalize(name)
	for _, re := range res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

type Stats struct {
	Characters int
	Metadata   int // memberships from a LoRA
	Lineage    int // videos that inherit a character from their reference image
	Starts     int // videos whose start image was found
}

// Refresh recomputes the automatic memberships of an owner's characters, and the start image of
// each of their image-to-video clips ("Made from").
func Refresh(ctx context.Context, s *store.Store, owner uuid.UUID) (Stats, error) {
	q := s.Q
	var st Stats
	rules, err := q.CharacterRules(ctx, owner)
	if err != nil {
		return st, err
	}
	st.Characters = len(rules)
	loras, err := q.LoraNames(ctx)
	if err != nil {
		return st, err
	}
	// Which LoRAs are whose.
	byLora := map[uuid.UUID][]uuid.UUID{}
	var loraIDs []uuid.UUID
	for _, r := range rules {
		res, err := Compile(r.Loras)
		if err != nil {
			continue // a bad pattern matches nothing; the editor refuses them
		}
		for _, l := range loras {
			if Matches(res, l.Name) {
				if len(byLora[l.ID]) == 0 {
					loraIDs = append(loraIDs, l.ID)
				}
				byLora[l.ID] = append(byLora[l.ID], r.ID)
			}
		}
	}
	// Items made with those LoRAs.
	type pair struct{ character, asset uuid.UUID }
	members := map[pair]bool{}
	byAsset := map[uuid.UUID][]uuid.UUID{}
	if len(loraIDs) > 0 {
		rows, err := q.LoraAssets(ctx, db.LoraAssetsParams{Ids: loraIDs, OwnerID: owner})
		if err != nil {
			return st, err
		}
		for _, r := range rows {
			for _, c := range byLora[r.LoraID] {
				p := pair{c, r.AssetID}
				if !members[p] {
					members[p] = true
					byAsset[r.AssetID] = append(byAsset[r.AssetID], c)
				}
			}
		}
	}
	st.Metadata = len(members)

	// Lineage: a video made from an image of a character shows that character. A reference names
	// its start image by file name; the candidates are the AI images with that name taken before
	// the video, and the one that looks most like the video wins, when it's close (the video starts
	// from it) and clearly closer than any other. Names recur (ComfyUI_00001_.png, image1.png), so
	// the look is what settles it. Until looks can be compared, only a name that's unique in the
	// whole library counts.
	names, err := q.ImageNames(ctx, owner)
	if err != nil {
		return st, err
	}
	type image struct {
		asset uuid.UUID
		taken time.Time
		ai    bool
	}
	byName := map[string]map[uuid.UUID]image{}
	for _, n := range names {
		b := path.Base(n.RelPath)
		if byName[b] == nil {
			byName[b] = map[uuid.UUID]image{}
		}
		byName[b][n.AssetID] = image{n.AssetID, n.TakenAt, n.Ai}
	}
	videos, err := q.LineageVideos(ctx, owner)
	if err != nil {
		return st, err
	}
	type link struct{ image, video uuid.UUID }
	candidates := map[uuid.UUID][]uuid.UUID{} // video -> its possible start images
	unique := map[link]bool{}                 // where the image's name is unique in the library
	var pairA, pairB []uuid.UUID
	for _, v := range videos {
		if !provenance.UsesStartImage(v.Model) {
			continue
		}
		seen := map[uuid.UUID]bool{}
		for _, ref := range provenance.Refs(v.Graph.String, v.Workflow.String) {
			found := byName[ref]
			for _, img := range found {
				if !img.ai || img.taken.After(v.TakenAt) || seen[img.asset] {
					continue
				}
				seen[img.asset] = true
				candidates[v.ID] = append(candidates[v.ID], img.asset)
				pairA, pairB = append(pairA, v.ID), append(pairB, img.asset)
				if len(found) == 1 {
					unique[link{img.asset, v.ID}] = true
				}
			}
		}
	}
	look := map[link]float32{}
	if len(pairA) > 0 {
		rows, err := q.LookAlike(ctx, db.LookAlikeParams{A: pairA, B: pairB, Model: search.Model})
		if err != nil {
			return st, err
		}
		for _, r := range rows {
			look[link{r.B, r.A}] = r.Sim
		}
	}
	lineage := map[pair]bool{}
	var startVideos, startImages []uuid.UUID
	var startLooks []float32 // NaN where the name alone was enough
	for video, imgs := range candidates {
		sims := map[uuid.UUID]float32{}
		for _, img := range imgs {
			if x, ok := look[link{img, video}]; ok {
				sims[img] = x
			}
		}
		start := StartImage(imgs, sims, func(img uuid.UUID) bool { return unique[link{img, video}] })
		if start == uuid.Nil {
			continue
		}
		x, ok := sims[start]
		if !ok {
			x = float32(math.NaN())
		}
		startVideos, startImages, startLooks = append(startVideos, video), append(startImages, start), append(startLooks, x)
		for _, c := range byAsset[start] {
			if p := (pair{c, video}); !members[p] {
				lineage[p] = true
			}
		}
	}
	st.Lineage = len(lineage)
	st.Starts = len(startVideos)

	err = s.Tx(ctx, func(tq *db.Queries) error {
		if err := tq.ClearStarts(ctx, owner); err != nil {
			return err
		}
		for i := 0; i < len(startVideos); i += 5000 {
			j := min(i+5000, len(startVideos))
			if err := tq.AddStarts(ctx, db.AddStartsParams{VideoIds: startVideos[i:j], ImageIds: startImages[i:j], Looks: startLooks[i:j]}); err != nil {
				return err
			}
		}
		if err := tq.ClearAutomaticMembers(ctx, owner); err != nil {
			return err
		}
		for basis, set := range map[string]map[pair]bool{"metadata": members, "lineage": lineage} {
			cs, as := make([]uuid.UUID, 0, len(set)), make([]uuid.UUID, 0, len(set))
			for p := range set {
				cs, as = append(cs, p.character), append(as, p.asset)
			}
			for i := 0; i < len(cs); i += 5000 {
				j := min(i+5000, len(cs))
				if err := tq.AddMembers(ctx, db.AddMembersParams{CharacterIds: cs[i:j], AssetIds: as[i:j], Basis: basis}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return st, err
}

// Name makes a readable name from a character's LoRA patterns, as the prototype did: the longest
// pattern's words, without version and base-model tags or the "ohwx" trigger.
var (
	trigger   = regexp.MustCompile(`(?i)ohwx`)
	separator = regexp.MustCompile(`[-_.$\s]+`)
	tag       = regexp.MustCompile(`(?i)^(v\d+|sdxl|sd15|flux|original|lora|xl)$`)
)

func Name(patterns []string) string {
	words := func(p string) []string {
		p = strings.TrimPrefix(p, "^")
		p = trigger.ReplaceAllString(p, " ")
		var out []string
		for _, w := range separator.Split(p, -1) {
			if len(w) > 1 && !tag.MatchString(w) {
				out = append(out, strings.ToUpper(w[:1])+strings.ToLower(w[1:]))
			}
		}
		return out
	}
	var best []string
	for _, p := range patterns {
		if w := words(p); len(w) > len(best) {
			best = w
		}
	}
	if len(best) == 0 && len(patterns) > 0 {
		return patterns[0]
	}
	return strings.Join(best, " ")
}

// How a video's start image is told apart from other images with the same name: by look, close
// (the video begins with it) and clearly closer than the next.
const (
	startMinLook = 0.80
	startLead    = 0.05
)

// StartImage picks a video's start image among its candidates, given how alike each looks
// (missing when it can't be compared yet). With no looks to compare, only a lone candidate whose
// name is unique in the library counts. It returns uuid.Nil when none can be picked.
func StartImage(candidates []uuid.UUID, look map[uuid.UUID]float32, uniqueName func(uuid.UUID) bool) uuid.UUID {
	if len(look) == 0 {
		if len(candidates) == 1 && uniqueName(candidates[0]) {
			return candidates[0]
		}
		return uuid.Nil
	}
	best, bestSim, second := uuid.Nil, float32(-1), float32(-1)
	for _, c := range candidates {
		x, ok := look[c]
		if !ok {
			continue
		}
		if x > bestSim {
			best, bestSim, second = c, x, bestSim
		} else if x > second {
			second = x
		}
	}
	if bestSim < startMinLook || (second >= 0 && bestSim-second < startLead) {
		return uuid.Nil
	}
	return best
}

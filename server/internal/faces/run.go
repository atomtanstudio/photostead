package faces

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

// The strict quality gate the benchmark chose for founding people: faces under 64 px, a detector
// score below 0.8, a yaw over 0.3, or among the blurriest tenth of the library.
const (
	gatePx    = 64
	gateScore = 0.8
	gateYaw   = 0.3
	gateBlur  = 10 // percentile
)

// Stats says what a grouping run did.
type Stats struct {
	Faces   int // faces in real photos
	Placed  int // newly placed this run
	People  int // people with faces
	New     int // people founded this run
	Named   int // of those, given a name from an import's suggestions
	Waiting int // faces not in a person (yet)

	Pets       int // cats and dogs in real photos
	PetGroups  int // pets they make up
	PetWaiting int // animals not in a pet (yet)
}

// Animals: below petMinScore a detection is left out of grouping (on this library, search agreed
// that fewer than a quarter of them show a cat or dog at all); founding a pet takes a large, sure
// detection. (The face measures, yaw and blur, don't apply to a whole animal.)
const (
	petMinScore  = 0.5
	petGatePx    = 48
	petGateScore = 0.7
)

// Vector decodes pgvector's binary halfvec (halfvec_send): a 2-byte dimension count, 2 unused
// bytes, then IEEE half floats, all big-endian. The result is scaled to unit length.
func Vector(b []byte) ([]float32, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("halfvec: %d bytes", len(b))
	}
	dim := int(binary.BigEndian.Uint16(b))
	if len(b) != 4+2*dim {
		return nil, fmt.Errorf("halfvec: %d bytes for %d dimensions", len(b), dim)
	}
	v := make([]float32, dim)
	var n float64
	for i := range v {
		v[i] = half(binary.BigEndian.Uint16(b[4+2*i:]))
		n += float64(v[i]) * float64(v[i])
	}
	if n > 0 {
		s := float32(1 / math.Sqrt(n))
		for i := range v {
			v[i] *= s
		}
	}
	return v, nil
}

// half converts an IEEE 754 half-precision float to float32.
func half(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h) & 0x3ff
	switch {
	case exp == 0 && frac == 0:
		return math.Float32frombits(sign)
	case exp == 0: // subnormal
		for frac&0x400 == 0 {
			frac <<= 1
			exp--
		}
		exp++
		frac &= 0x3ff
	case exp == 0x1f:
		return math.Float32frombits(sign | 0x7f800000 | frac<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | frac<<13)
}

// percentile is numpy's default (linear interpolation between the closest ranks).
func percentile(xs []float32, p float64) float32 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float32(nil), xs...)
	sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
	pos := p / 100 * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := min(lo+1, len(s)-1)
	return s[lo] + float32(pos-float64(lo))*(s[hi]-s[lo])
}

// Run groups an owner's faces into people, then each species' animals into pets (each on its own,
// with its own settings): new faces join people or wait, the waiting ones found people when enough
// of them agree, and each new person is named from an import when its photos are the ones the
// import suggested for a named person.
func Run(ctx context.Context, s *store.Store, owner uuid.UUID, log *slog.Logger) (Stats, error) {
	var st Stats
	if err := s.Q.ReleaseOrphanFaces(ctx, owner); err != nil {
		return st, err
	}
	for _, kind := range []struct {
		species string
		p       Params
	}{{"", PeopleParams}, {"cat", PetParams}, {"dog", PetParams}} {
		k, err := group(ctx, s, owner, kind.species, kind.p)
		if err != nil {
			return st, err
		}
		if kind.species == "" {
			st = k
			continue
		}
		st.Pets += k.Faces
		st.PetGroups += k.People
		st.PetWaiting += k.Waiting
		st.Placed += k.Placed
		st.New += k.New
	}
	if err := s.Q.SetCovers(ctx, owner); err != nil {
		return st, err
	}
	if err := s.Q.DropEmptyFacePeople(ctx, owner); err != nil {
		return st, err
	}
	if err := s.Q.SyncFaceCharacters(ctx, owner); err != nil {
		return st, err
	}
	if log != nil {
		log.Info("faces grouped", "faces", st.Faces, "placed", st.Placed, "people", st.People, "new", st.New, "named", st.Named,
			"waiting", st.Waiting, "pets", st.Pets, "pet groups", st.PetGroups)
	}
	return st, s.Q.RecordFaceRun(ctx, db.RecordFaceRunParams{OwnerID: owner, Faces: int32(st.Faces + st.Pets), People: int32(st.People + st.PetGroups)})
}

// group groups one kind of face: people's (species "") or one species of pet.
func group(ctx context.Context, s *store.Store, owner uuid.UUID, species string, p Params) (Stats, error) {
	var st Stats
	all, err := s.Q.ClusterFaces(ctx, db.ClusterFacesParams{OwnerID: owner, Species: pgtype.Text{String: species, Valid: species != ""}})
	if err != nil {
		return st, err
	}
	rows := all[:0]
	var unsure []uuid.UUID // weak detections grouping placed before (none should be)
	for _, r := range all {
		if species != "" && r.Score < petMinScore {
			if r.PersonID.Valid && r.Assignment.String == "auto" {
				unsure = append(unsure, r.ID)
			}
			continue
		}
		rows = append(rows, r)
	}
	if len(unsure) > 0 {
		if err := s.Q.UnplaceFaces(ctx, unsure); err != nil {
			return st, err
		}
	}
	st.Faces = len(rows)
	if len(rows) == 0 {
		return st, nil
	}
	blur := make([]float32, len(rows))
	for i, r := range rows {
		blur[i] = r.Blur
	}
	blurCut := percentile(blur, gateBlur)

	labelOf := map[uuid.UUID]int{}
	var people []uuid.UUID // label -> person
	label := func(p uuid.UUID) int {
		l, ok := labelOf[p]
		if !ok {
			l = len(people)
			labelOf[p] = l
			people = append(people, p)
		}
		return l
	}
	faces := make([]Face, len(rows))
	for i, r := range rows {
		v, err := Vector(r.Vec)
		if err != nil {
			return st, fmt.Errorf("face %s: %w", r.ID, err)
		}
		f := Face{Vec: v, Label: -1, Seen: r.Seen}
		if species == "" {
			f.Gated = r.Px < gatePx || r.Score < gateScore || float32(math.Abs(float64(r.Yaw))) > gateYaw || r.Blur < blurCut
		} else {
			f.Gated = r.Px < petGatePx || r.Score < petGateScore
		}
		if r.PersonID.Valid {
			f.Label = label(r.PersonID.UUID)
			f.Fixed = r.Assignment.String == "confirmed"
		}
		for _, p := range r.Rejected {
			f.Reject = append(f.Reject, label(p))
		}
		faces[i] = f
	}
	known := len(people)
	before := make([]int, len(faces))
	for i := range faces {
		before[i] = faces[i].Label
	}
	total := GroupWith(faces, known, p)

	// Someone new must be seen in two items at least: two faces (or animals) in one picture are two
	// different people (or pets), however alike they look.
	seenIn := map[int]map[uuid.UUID]bool{}
	for i, f := range faces {
		if f.Label >= known {
			if seenIn[f.Label] == nil {
				seenIn[f.Label] = map[uuid.UUID]bool{}
			}
			seenIn[f.Label][rows[i].AssetID] = true
		}
	}
	for i := range faces {
		if l := faces[i].Label; l >= known && len(seenIn[l]) < 2 {
			faces[i].Label = -1
		}
	}

	// New people: named from an import when most of their photos are a named person's suggestions.
	// (Imports name people only, never pets.)
	suggested := map[uuid.UUID][]uuid.UUID{} // asset -> named people
	if total > known && species == "" {
		sugg, err := s.Q.NamedSuggestions(ctx, owner)
		if err != nil {
			return st, err
		}
		for _, r := range sugg {
			suggested[r.AssetID] = append(suggested[r.AssetID], r.PersonID)
		}
	}
	members := map[int][]int{}
	for i, f := range faces {
		if f.Label >= known {
			members[f.Label] = append(members[f.Label], i)
		}
	}
	for l := known; l < total; l++ {
		if len(members[l]) == 0 { // not founded after all
			people = append(people, uuid.Nil)
			continue
		}
		votes := map[uuid.UUID]int{}
		for _, i := range members[l] {
			for _, p := range suggested[rows[i].AssetID] {
				votes[p]++
			}
		}
		var named uuid.UUID
		for p, n := range votes {
			if n >= 3 && 2*n >= len(members[l]) && (named == uuid.Nil || n > votes[named]) {
				named = p
			}
		}
		st.New++
		if named != uuid.Nil {
			people = append(people, named)
			st.Named++
			continue
		}
		id, err := s.Q.NewFacePerson(ctx, db.NewFacePersonParams{OwnerID: owner, Species: pgtype.Text{String: species, Valid: species != ""}})
		if err != nil {
			return st, err
		}
		people = append(people, id)
	}

	var ids, to, seen []uuid.UUID
	for i, f := range faces {
		if f.Label >= 0 && f.Label != before[i] {
			ids, to = append(ids, rows[i].ID), append(to, people[f.Label])
		}
		if f.Label < 0 {
			st.Waiting++
		}
		if !rows[i].Seen {
			seen = append(seen, rows[i].ID)
		}
	}
	st.Placed = len(ids)
	for i := 0; i < len(ids); i += 5000 {
		j := min(i+5000, len(ids))
		if err := s.Q.AssignFaces(ctx, db.AssignFacesParams{FaceIds: ids[i:j], PersonIds: to[i:j]}); err != nil {
			return st, err
		}
	}
	for i := 0; i < len(seen); i += 5000 {
		if err := s.Q.MarkGrouped(ctx, seen[i:min(i+5000, len(seen))]); err != nil {
			return st, err
		}
	}
	withFaces := map[uuid.UUID]bool{}
	for _, f := range faces {
		if f.Label >= 0 {
			withFaces[people[f.Label]] = true
		}
	}
	st.People = len(withFaces)
	return st, nil
}

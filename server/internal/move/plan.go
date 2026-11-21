// Package move plans (and, later, carries out) moving a person's originals into the managed
// share: library/<person>/photos|videos/<year>/<date>/<file>. See docs/architecture.md,
// "Storage layout".
package move

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Source is one item and the copy of it that will be moved.
type Source struct {
	AssetID    uuid.UUID
	Type       string // image or video
	Day        time.Time
	TakenAt    time.Time
	LiveMotion uuid.NullUUID // a still's motion clip
	MotionOf   uuid.NullUUID // a clip's still
	StackCover uuid.NullUUID // the top item of its stack, if any
	FileID     uuid.UUID
	LibraryID  uuid.UUID
	Library    string
	Rel        string
	Size       int64
	Copies     int   // files with these exact bytes, including this one
	OtherBytes int64 // bytes in the other copies, which stay where they are
}

// Copy is one of an item's files (the item may have several identical copies).
type Copy struct {
	LibraryID uuid.UUID
	Rel       string
}

// Sidecar is an XMP file found next to the originals.
type Sidecar struct {
	FileID    uuid.UUID
	LibraryID uuid.UUID
	Library   string
	Rel       string
	Size      int64
}

// Entry is one file to copy: an item's original, a Live Photo's motion clip, or a sidecar.
type Entry struct {
	Role      string // original, motion or sidecar
	AssetID   uuid.NullUUID
	FileID    uuid.UUID
	LibraryID uuid.UUID
	Library   string
	Src       string // relative to its library
	Dst       string // relative to the managed share
	Size      int64
	Renamed   bool // the name was taken in that folder, so a number was added
}

// Build places every item. Photos go to photos/, videos to videos/, by the day they were taken.
// A Live Photo's clip goes with its still (into photos/, named after the still); the other
// items of a stack go to the cover's day. Names stay as they are unless another item already
// has that name in the same folder (compared without case, as Windows and macOS do), in which
// case "-2", "-3" … is added. Sidecars follow their item as <name>.xmp, whichever copy they sit
// beside; if several copies have one, the moved copy's own wins and the rest are reported as
// spares.
func Build(person string, sources []Source, copies map[uuid.UUID][]Copy, sidecars []Sidecar) (entries []Entry, unmatched, spare []Sidecar) {
	byID := make(map[uuid.UUID]*Source, len(sources))
	for i := range sources {
		byID[sources[i].AssetID] = &sources[i]
	}
	folderOf := func(s *Source) string {
		kind, day := "videos", s.Day
		if s.Type == "image" {
			kind = "photos"
		}
		if s.MotionOf.Valid {
			if still, ok := byID[s.MotionOf.UUID]; ok {
				kind, day = "photos", still.Day
			}
		}
		if s.StackCover.Valid && s.StackCover.UUID != s.AssetID {
			if cover, ok := byID[s.StackCover.UUID]; ok {
				day = cover.Day
			}
		}
		return fmt.Sprintf("library/%s/%s/%04d/%s", person, kind, day.Year(), day.Format("2006-01-02"))
	}

	// Items first, in a stable order, so the earliest item in a folder keeps a contested name.
	order := make([]*Source, 0, len(sources))
	var clips []*Source
	for i := range sources {
		s := &sources[i]
		if s.MotionOf.Valid && byID[s.MotionOf.UUID] != nil {
			clips = append(clips, s)
			continue
		}
		order = append(order, s)
	}
	folders := make(map[uuid.UUID]string, len(sources))
	for _, s := range sources {
		folders[s.AssetID] = folderOf(byID[s.AssetID])
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if folders[a.AssetID] != folders[b.AssetID] {
			return folders[a.AssetID] < folders[b.AssetID]
		}
		if !a.TakenAt.Equal(b.TakenAt) {
			return a.TakenAt.Before(b.TakenAt)
		}
		return a.AssetID.String() < b.AssetID.String()
	})

	taken := map[string]bool{}
	claim := func(folder, name string) (string, bool) {
		ext := path.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		for n := 1; ; n++ {
			candidate := name
			if n > 1 {
				candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
			}
			key := strings.ToLower(folder + "/" + candidate)
			if !taken[key] {
				taken[key] = true
				return candidate, n > 1
			}
		}
	}
	dstOf := map[uuid.UUID]string{}
	add := func(s *Source, role, name string) {
		folder := folders[s.AssetID]
		final, renamed := claim(folder, name)
		dst := folder + "/" + final
		dstOf[s.AssetID] = dst
		entries = append(entries, Entry{Role: role, AssetID: uuid.NullUUID{UUID: s.AssetID, Valid: true}, FileID: s.FileID,
			LibraryID: s.LibraryID, Library: s.Library, Src: s.Rel, Dst: dst, Size: s.Size, Renamed: renamed})
	}
	for _, s := range order {
		add(s, "original", path.Base(s.Rel))
	}
	sort.SliceStable(clips, func(i, j int) bool { return clips[i].AssetID.String() < clips[j].AssetID.String() })
	for _, c := range clips {
		still := dstOf[c.MotionOf.UUID]
		stem := strings.TrimSuffix(path.Base(still), path.Ext(still))
		add(c, "motion", stem+path.Ext(c.Rel)) // IMG_0001.HEIC + IMG_0001.MOV stay a matching pair
	}

	// Sidecars: name.ext.xmp first, then name.xmp; each sidecar travels once.
	type key struct {
		lib uuid.UUID
		p   string
	}
	index := map[key][]int{}
	for i, sc := range sidecars {
		base := strings.ToLower(strings.TrimSuffix(sc.Rel, path.Ext(sc.Rel)))
		index[key{sc.LibraryID, base}] = append(index[key{sc.LibraryID, base}], i)
	}
	used := make([]bool, len(sidecars))
	isSpare := make([]bool, len(sidecars))
	has := map[uuid.UUID]bool{} // items that already have a sidecar travelling
	n := len(entries)
	placesOf := func(e Entry) []Copy {
		places := []Copy{{e.LibraryID, e.Src}} // the moved copy first, then the item's other copies
		if e.Role == "motion" {
			return places // a clip's name.xmp would be its still's sidecar
		}
		for _, c := range copies[e.AssetID.UUID] {
			if c.LibraryID != e.LibraryID || c.Rel != e.Src {
				places = append(places, c)
			}
		}
		return places
	}
	attach := func(e Entry, k key) {
		for _, si := range index[k] {
			if used[si] {
				continue
			}
			used[si] = true
			if has[e.AssetID.UUID] {
				isSpare[si] = true
				continue
			}
			has[e.AssetID.UUID] = true
			sc := sidecars[si]
			entries = append(entries, Entry{Role: "sidecar", AssetID: e.AssetID, FileID: sc.FileID, LibraryID: sc.LibraryID,
				Library: sc.Library, Src: sc.Rel, Dst: e.Dst + ".xmp", Size: sc.Size})
			taken[strings.ToLower(e.Dst+".xmp")] = true
		}
	}
	// Pass 1: name.ext.xmp is unambiguous.
	for i := 0; i < n; i++ {
		for _, c := range placesOf(entries[i]) {
			attach(entries[i], key{c.LibraryID, strings.ToLower(c.Rel)})
		}
	}
	// Pass 2: name.xmp. When a RAW and a JPEG share the name, it belongs to the RAW (the
	// convention of Lightroom and darktable); otherwise to the first file in plan order.
	byRawFirst := make([]int, n)
	for i := range byRawFirst {
		byRawFirst[i] = i
	}
	sort.SliceStable(byRawFirst, func(a, b int) bool {
		return isRaw(entries[byRawFirst[a]].Src) && !isRaw(entries[byRawFirst[b]].Src)
	})
	for _, i := range byRawFirst {
		for _, c := range placesOf(entries[i]) {
			src := strings.ToLower(c.Rel)
			attach(entries[i], key{c.LibraryID, strings.TrimSuffix(src, path.Ext(src))})
		}
	}
	for i, sc := range sidecars {
		switch {
		case isSpare[i]:
			spare = append(spare, sc)
		case !used[i]:
			unmatched = append(unmatched, sc)
		}
	}
	return entries, unmatched, spare
}

var rawExt = map[string]bool{".dng": true, ".cr2": true, ".cr3": true, ".crw": true, ".nef": true, ".nrw": true, ".arw": true,
	".srf": true, ".sr2": true, ".orf": true, ".rw2": true, ".raf": true, ".pef": true, ".srw": true, ".3fr": true, ".erf": true,
	".kdc": true, ".mrw": true, ".x3f": true, ".iiq": true, ".raw": true}

func isRaw(name string) bool { return rawExt[strings.ToLower(path.Ext(name))] }

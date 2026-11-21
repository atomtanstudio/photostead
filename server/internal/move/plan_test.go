package move

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func day(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

func TestBuild(t *testing.T) {
	lib := uuid.New()
	id := func() uuid.UUID { return uuid.New() }
	still, clip, raw, jpeg := id(), id(), id(), id()
	a1, a2, vid := id(), id(), id()
	sources := []Source{
		{AssetID: a1, Type: "image", Day: day("2024-05-01"), TakenAt: day("2024-05-01").Add(time.Hour), LibraryID: lib, Rel: "trip/beach.jpg"},
		{AssetID: a2, Type: "image", Day: day("2024-05-01"), TakenAt: day("2024-05-01").Add(2 * time.Hour), LibraryID: lib, Rel: "other/Beach.JPG"},
		{AssetID: vid, Type: "video", Day: day("2023-08-12"), LibraryID: lib, Rel: "clips/clip.mp4"},
		{AssetID: still, Type: "image", Day: day("2022-01-02"), LiveMotion: uuid.NullUUID{UUID: clip, Valid: true}, LibraryID: lib, Rel: "phone/IMG_0001.HEIC"},
		{AssetID: clip, Type: "video", Day: day("2022-01-03"), MotionOf: uuid.NullUUID{UUID: still, Valid: true}, LibraryID: lib, Rel: "phone/IMG_0001.MOV"},
		{AssetID: jpeg, Type: "image", Day: day("2021-06-06"), StackCover: uuid.NullUUID{UUID: jpeg, Valid: true}, LibraryID: lib, Rel: "cam/DSC_1.JPG"},
		{AssetID: raw, Type: "image", Day: day("2021-06-07"), StackCover: uuid.NullUUID{UUID: jpeg, Valid: true}, LibraryID: lib, Rel: "cam/DSC_1.NEF"},
	}
	sidecars := []Sidecar{
		{LibraryID: lib, Rel: "trip/beach.jpg.xmp"},
		{LibraryID: lib, Rel: "cam/DSC_1.xmp"},
		{LibraryID: lib, Rel: "orphans/gone.jpg.xmp"},
	}
	copies := map[uuid.UUID][]Copy{a2: {{lib, "other/Beach.JPG"}, {lib, "dupes/Beach.JPG"}}}
	sidecars = append(sidecars, Sidecar{LibraryID: lib, Rel: "dupes/Beach.JPG.xmp"}) // beside a copy that isn't moved
	entries, unmatched, spare := Build("rich", sources, copies, sidecars)
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Src] = e
	}
	want := map[string]string{
		"trip/beach.jpg":      "library/rich/photos/2024/2024-05-01/beach.jpg",
		"other/Beach.JPG":     "library/rich/photos/2024/2024-05-01/Beach-2.JPG", // same name ignoring case: numbered
		"clips/clip.mp4":      "library/rich/videos/2023/2023-08-12/clip.mp4",
		"phone/IMG_0001.HEIC": "library/rich/photos/2022/2022-01-02/IMG_0001.HEIC",
		"phone/IMG_0001.MOV":  "library/rich/photos/2022/2022-01-02/IMG_0001.MOV", // with its still, in photos/
		"cam/DSC_1.JPG":       "library/rich/photos/2021/2021-06-06/DSC_1.JPG",
		"cam/DSC_1.NEF":       "library/rich/photos/2021/2021-06-06/DSC_1.NEF", // the cover's day
		"trip/beach.jpg.xmp":  "library/rich/photos/2024/2024-05-01/beach.jpg.xmp",
		"cam/DSC_1.xmp":       "library/rich/photos/2021/2021-06-06/DSC_1.NEF.xmp",   // name.xmp belongs to the RAW
		"dupes/Beach.JPG.xmp": "library/rich/photos/2024/2024-05-01/Beach-2.JPG.xmp", // from another copy of the item
	}
	for src, dst := range want {
		if got[src].Dst != dst {
			t.Errorf("%s -> %q, want %q", src, got[src].Dst, dst)
		}
	}
	if !got["other/Beach.JPG"].Renamed || got["trip/beach.jpg"].Renamed {
		t.Error("only the later of two same-named items should be renamed")
	}
	if got["phone/IMG_0001.MOV"].Role != "motion" || got["trip/beach.jpg.xmp"].Role != "sidecar" {
		t.Errorf("roles: %q %q", got["phone/IMG_0001.MOV"].Role, got["trip/beach.jpg.xmp"].Role)
	}
	if len(unmatched) != 1 || unmatched[0].Rel != "orphans/gone.jpg.xmp" || len(spare) != 0 {
		t.Errorf("unmatched = %+v, spare = %+v", unmatched, spare)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Dst] {
			t.Errorf("two files planned for %s", e.Dst)
		}
		seen[e.Dst] = true
	}
}

func TestRenamedClipFollowsItsStill(t *testing.T) {
	lib := uuid.New()
	other, still, clip := uuid.New(), uuid.New(), uuid.New()
	d := day("2022-01-02")
	sources := []Source{
		{AssetID: other, Type: "image", Day: d, TakenAt: d, LibraryID: lib, Rel: "a/IMG_0001.HEIC"},
		{AssetID: still, Type: "image", Day: d, TakenAt: d.Add(time.Minute), LibraryID: lib, Rel: "b/IMG_0001.HEIC", LiveMotion: uuid.NullUUID{UUID: clip, Valid: true}},
		{AssetID: clip, Type: "video", Day: d, LibraryID: lib, Rel: "b/IMG_0001.MOV", MotionOf: uuid.NullUUID{UUID: still, Valid: true}},
	}
	entries, _, _ := Build("rich", sources, nil, nil)
	for _, e := range entries {
		if e.Src == "b/IMG_0001.MOV" && e.Dst != "library/rich/photos/2022/2022-01-02/IMG_0001-2.MOV" {
			t.Errorf("clip -> %s, want it to match its renamed still IMG_0001-2", e.Dst)
		}
	}
}

package sidecar

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store/db"
)

func TestTags(t *testing.T) {
	r := db.SidecarItemsRow{Rating: 4, ColorLabel: pgtype.Text{String: "purple", Valid: true}, Keywords: []string{"travel/japan", "sunset"},
		Description: pgtype.Text{String: "two\nlines", Valid: true}, DateSource: "user", TakenAt: time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC),
		TakenOffsetMin: pgtype.Int2{Int16: -420, Valid: true}, People: []string{"Mia"}, SourceType: "ai_generated"}
	tags, worth := Tags(r)
	got := strings.Join(tags, "\n")
	for _, want := range []string{"-XMP-xmp:Rating=4", "-XMP-xmp:Label=Purple", "-XMP-dc:Subject=japan", "-XMP-dc:Subject=sunset",
		"-XMP-lr:HierarchicalSubject=travel|japan", "-XMP-photoshop:DateCreated=2024:05:01 05:00:00-07:00", "-XMP-iptcExt:PersonInImage=Mia",
		"-XMP-iptcExt:DigitalSourceType=" + trainedMedia, "-XMP-photostead:Favorite="} {
		if !strings.Contains(got, want+"\n") && !strings.HasSuffix(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if !worth {
		t.Error("marks and keywords are worth a sidecar")
	}
	if _, worth := Tags(db.SidecarItemsRow{SourceType: "ai_generated", Prompt: pgtype.Text{String: "a cat", Valid: true}}); worth {
		t.Error("an AI item's own metadata alone isn't worth a sidecar: it's in the file")
	}
	reject, _ := Tags(db.SidecarItemsRow{Rating: 3, Flag: pgtype.Text{String: "reject", Valid: true}})
	if !strings.Contains(strings.Join(reject, "\n"), "-XMP-xmp:Rating=-1") {
		t.Error("a reject is -1 stars, as in Lightroom")
	}
	if escape("a\\b\nc\r") != `a\\b\nc` {
		t.Errorf("escape = %q", escape("a\\b\nc\r"))
	}
}

func TestPath(t *testing.T) {
	lib := uuid.MustParse("01a14816-c8cb-7449-998b-7be36885944c")
	row := func(mode string, readOnly bool, rel string) db.SidecarItemsRow {
		return db.SidecarItemsRow{RelPath: pgtype.Text{String: rel, Valid: true}, RootPath: pgtype.Text{String: "/photos/lib", Valid: true},
			LibraryID: uuid.NullUUID{UUID: lib, Valid: true}, SidecarMode: pgtype.Text{String: mode, Valid: true}, ReadOnly: pgtype.Bool{Bool: readOnly, Valid: true}}
	}
	for _, c := range []struct {
		r    db.SidecarItemsRow
		want string
	}{
		{row("beside", false, "photos/2024/a.jpg"), "/photos/lib/photos/2024/a.jpg.xmp"},
		{row("beside", true, "a.jpg"), "/data/sidecars/" + lib.String() + "/a.jpg.xmp"}, // never into a read-only library
		{row("shadow", false, "x/a.CR2"), "/data/sidecars/" + lib.String() + "/x/a.CR2.xmp"},
		{row("off", false, "a.jpg"), ""},
		{row("beside", false, "../escape.jpg"), ""},
		{row("beside", false, "bad\nname.jpg"), ""},
	} {
		if got := Path(c.r, "/data"); got != c.want {
			t.Errorf("Path(%s, %s) = %q, want %q", c.r.SidecarMode.String, c.r.RelPath.String, got, c.want)
		}
	}
}

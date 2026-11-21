package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestRenderFixtures(t *testing.T) {
	if err := Start(2); err != nil {
		t.Fatal(err)
	}
	r := Renderer{DataDir: t.TempDir()}
	cases := []struct {
		file  string
		video bool
		wide  bool // landscape after orientation
	}{
		{"photo-tokyo.jpg", false, false}, // stored 1200x800 with EXIF orientation 6
		{"generated.png", false, false},
		{"still.heic", false, false},
		{"clip.mp4", true, true},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			id := uuid.New()
			path := "../testdata/library/" + c.file
			var res Result
			var err error
			if c.video {
				res, err = r.Video(context.Background(), path, id, 2000)
			} else {
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				res, err = r.Image(data, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if (res.Width > res.Height) != c.wide {
				t.Errorf("rendered %dx%d, want wide=%v", res.Width, res.Height, c.wide)
			}
			if len(res.ThumbHash) < 5 {
				t.Errorf("thumbhash = %x", res.ThumbHash)
			}
			thumb, preview := r.Paths(id)
			for _, p := range []string{thumb, preview} {
				if info, err := os.Stat(p); err != nil || info.Size() == 0 {
					t.Errorf("%s: %v", p, err)
				}
			}
		})
	}
}

func TestPosterTimes(t *testing.T) {
	for _, c := range []struct {
		ms   int
		want []float64
	}{
		{0, []float64{1, 5, 15}},
		{1000, []float64{0.5, 0.25, 0.75}},
		{8000, []float64{1, 2, 4}},
		{240_000, []float64{1, 24, 60, 120}},
	} {
		if got := posterTimes(c.ms); !slices.Equal(got, c.want) {
			t.Errorf("posterTimes(%d) = %v, want %v", c.ms, got, c.want)
		}
	}
}

// A video that opens on three seconds of black gets a poster from later on; one that's black
// throughout still gets one.
func TestPosterSkipsBlack(t *testing.T) {
	if err := Start(2); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	video := func(name, filter string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if out, err := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", filter,
			"-t", "8", "-pix_fmt", "yuv420p", p).CombinedOutput(); err != nil {
			t.Fatalf("making %s: %v %s", name, err, out)
		}
		return p
	}
	opensBlack := video("opens-black.mp4", "testsrc=size=320x240:rate=10,drawbox=enable='lt(t,3)':color=black:t=fill")
	allBlack := video("all-black.mp4", "color=black:size=320x240:rate=10")
	r := Renderer{DataDir: dir}
	res, err := r.Video(context.Background(), opensBlack, uuid.New(), 8000)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := HashSpread(res.ThumbHash); s < FlatSpread {
		t.Errorf("the poster is blank (spread %.1f)", s)
	}
	res, err = r.Video(context.Background(), allBlack, uuid.New(), 8000)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := HashSpread(res.ThumbHash); s >= FlatSpread {
		t.Errorf("a black video's poster has a spread of %.1f", s)
	}
}

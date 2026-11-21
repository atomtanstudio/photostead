package stacks

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"go.n16f.net/thumbhash"
)

func TestAlike(t *testing.T) {
	photo, clip := look{}, look{video: true, duration: 60_000}
	for _, c := range []struct {
		name string
		a, b look
		want bool
	}{
		{"two photos", photo, photo, true},
		{"a blank photo", photo, look{blank: true}, false},
		{"two blank photos", look{blank: true}, look{blank: true}, false},
		{"a photo and a video", photo, clip, false},
		{"one video saved twice", clip, look{video: true, duration: 60_400}, true},
		{"1% longer", look{video: true, duration: 300_000}, look{video: true, duration: 303_000}, true},
		{"2% longer", look{video: true, duration: 300_000}, look{video: true, duration: 306_000}, false},
		{"a 4- and a 5-second clip", look{video: true, duration: 4_063}, look{video: true, duration: 5_032}, false},
		{"same intro, different videos", clip, look{video: true, duration: 75_000}, false},
		{"a video with no length", clip, look{video: true}, false},
		{"blank videos of one length", look{video: true, duration: 60_000, blank: true}, look{video: true, duration: 60_000, blank: true}, false},
	} {
		if got := alike(c.a, c.b); got != c.want {
			t.Errorf("%s: alike = %v", c.name, got)
		}
	}
}

func TestBlankByThumbHash(t *testing.T) {
	fill := func(paint func(x, y int) color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 64, 36))
		for y := 0; y < 36; y++ {
			for x := 0; x < 64; x++ {
				img.Set(x, y, paint(x, y))
			}
		}
		return thumbhash.EncodeImage(img)
	}
	black := fill(func(int, int) color.Color { return color.Black })
	card := fill(func(x, y int) color.Color { return color.RGBA{20, 20, 60, 255} })
	scene := fill(func(x, y int) color.Color { return color.RGBA{uint8(x * 4), uint8(y * 7), 90, 255} })
	if !lookOf("video", 1, black).blank || !lookOf("image", 0, card).blank {
		t.Error("a black frame or a plain card isn't blank")
	}
	if lookOf("image", 0, scene).blank {
		t.Error("a picture with something in it is blank")
	}
	if lookOf("image", 0, nil).blank || bytes.Equal(black, scene) {
		t.Error("no ThumbHash counts as blank")
	}
}

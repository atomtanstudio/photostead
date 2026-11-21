package media

import (
	"image"
	"math"

	"go.n16f.net/thumbhash"
)

// FlatSpread is the brightness spread (standard deviation, 0–255) below which a picture is
// treated as blank: a black or white frame, a fade, a plain title card. Blank pictures all look
// alike, so they're never look-alikes, and a video's poster avoids them.
const FlatSpread = 6

// Spread is the standard deviation of an image's brightness, 0–255.
func Spread(img image.Image) float64 {
	b := img.Bounds()
	var n, sum, sq float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			l := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257
			n++
			sum += l
			sq += l * l
		}
	}
	if n == 0 {
		return 0
	}
	mean := sum / n
	return math.Sqrt(math.Max(0, sq/n-mean*mean))
}

// Blank says whether a small image (ThumbHash's 100 px at most) is blank, judged as look-alikes
// judge an item: by its ThumbHash.
func Blank(small image.Image) bool {
	spread, ok := HashSpread(thumbhash.EncodeImage(small))
	return ok && spread < FlatSpread
}

// HashSpread is Spread for an item's ThumbHash (the placeholder kept for every item), so it works
// for items rendered before posters skipped blank frames. ok is false for a hash that won't decode.
func HashSpread(hash []byte) (spread float64, ok bool) {
	img, err := thumbhash.DecodeImage(hash)
	if err != nil {
		return 0, false
	}
	return Spread(img), true
}

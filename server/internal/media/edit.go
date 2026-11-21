package media

import (
	"errors"
	"fmt"
	"math"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/google/uuid"
)

// Recipe is a photo's edit: kept beside the original, which is never changed, and applied when its
// previews are made (and to the full picture for an edited download). Turning and flipping first,
// then the crop (in the turned picture, as shares of its width and height), then light and color.
type Recipe struct {
	Rotate     int     `json:"rotate" enum:"0,90,180,270" doc:"Turned clockwise, in degrees"`
	Flip       bool    `json:"flip,omitempty" doc:"Mirrored left to right"`
	Crop       *Rect   `json:"crop,omitempty" doc:"What's kept, in the turned picture: shares of its width and height (0 to 1)"`
	Exposure   float64 `json:"exposure,omitempty" minimum:"-2" maximum:"2" doc:"Lighter or darker, in stops"`
	Contrast   float64 `json:"contrast,omitempty" minimum:"-1" maximum:"1"`
	Saturation float64 `json:"saturation,omitempty" minimum:"-1" maximum:"1" doc:"-1 is black and white"`
	Warmth     float64 `json:"warmth,omitempty" minimum:"-1" maximum:"1" doc:"Warmer (above 0) or cooler"`
}

type Rect struct {
	X float64 `json:"x" minimum:"0" maximum:"1"`
	Y float64 `json:"y" minimum:"0" maximum:"1"`
	W float64 `json:"w" minimum:"0" maximum:"1"`
	H float64 `json:"h" minimum:"0" maximum:"1"`
}

// Empty is a recipe that changes nothing.
func (r Recipe) Empty() bool {
	return r.Rotate%360 == 0 && !r.Flip && (r.Crop == nil || r.Crop.whole()) && r.Exposure == 0 && r.Contrast == 0 && r.Saturation == 0 && r.Warmth == 0
}

func (c *Rect) whole() bool { return c.X <= 0 && c.Y <= 0 && c.W >= 1 && c.H >= 1 }

// Check refuses a recipe that can't be applied.
func (r Recipe) Check() error {
	switch r.Rotate {
	case 0, 90, 180, 270:
	default:
		return errors.New("rotate is 0, 90, 180 or 270")
	}
	if c := r.Crop; c != nil {
		if c.W < 0.02 || c.H < 0.02 || c.X < 0 || c.Y < 0 || c.X+c.W > 1.0001 || c.Y+c.H > 1.0001 {
			return errors.New("the crop must be inside the picture, and not tiny")
		}
	}
	for _, v := range []float64{r.Exposure / 2, r.Contrast, r.Saturation, r.Warmth} {
		if v < -1 || v > 1 || math.IsNaN(v) {
			return errors.New("exposure is -2 to 2 stops; contrast, saturation and warmth are -1 to 1")
		}
	}
	return nil
}

// Size is the edited picture's size, from the original's (already the right way up).
func (r Recipe) Size(w, h int) (int, int) {
	if r.Rotate == 90 || r.Rotate == 270 {
		w, h = h, w
	}
	if c := r.Crop; c != nil && !c.whole() {
		w, h = max(1, int(math.Round(float64(w)*c.W))), max(1, int(math.Round(float64(h)*c.H)))
	}
	return w, h
}

// ImageEdited renders a photo's previews with its edit (or as it is, with none).
func (r Renderer) ImageEdited(data []byte, id uuid.UUID, rec *Recipe) (Result, error) {
	if rec == nil || rec.Empty() {
		return r.Image(data, id)
	}
	// A crop keeps part of the picture: load it larger, so what's kept still fills a preview.
	size := previewSize
	if c := rec.Crop; c != nil && !c.whole() {
		size = min(4*previewSize, int(float64(previewSize)/max(0.25, min(c.W, c.H))))
	}
	img, err := vips.NewThumbnailWithSizeFromBuffer(data, size, size, vips.InterestingNone, vips.SizeDown)
	if err != nil {
		return Result{}, fmt.Errorf("decode: %w", err)
	}
	if err := apply(img, *rec); err != nil {
		img.Close()
		return Result{}, err
	}
	if img.Width() > previewSize || img.Height() > previewSize {
		if err := img.Thumbnail(previewSize, previewSize, vips.InterestingNone); err != nil {
			img.Close()
			return Result{}, err
		}
	}
	return r.finish(img, id)
}

// Unedited is a photo as it is, preview-sized, as a WebP: what the editor starts from.
func Unedited(data []byte) ([]byte, error) {
	img, err := vips.NewThumbnailWithSizeFromBuffer(data, previewSize, previewSize, vips.InterestingNone, vips.SizeDown)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	defer img.Close()
	if err := toSRGB(img); err != nil {
		return nil, err
	}
	out, _, err := img.ExportWebp(&vips.WebpExportParams{Quality: 85, StripMetadata: true, ReductionEffort: 2})
	return out, err
}

// Edited is the whole photo with its edit, as a JPEG (for downloading the edited version).
func Edited(data []byte, rec Recipe) ([]byte, error) {
	img, err := vips.NewImageFromBuffer(data)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	defer img.Close()
	if err := img.AutoRotate(); err != nil {
		return nil, err
	}
	if err := apply(img, rec); err != nil {
		return nil, err
	}
	if err := toSRGB(img); err != nil {
		return nil, err
	}
	out, _, err := img.ExportJpeg(&vips.JpegExportParams{Quality: 92, StripMetadata: true})
	return out, err
}

// apply does a recipe to a picture that's the right way up.
func apply(img *vips.ImageRef, rec Recipe) error {
	switch rec.Rotate {
	case 90:
		if err := img.Rotate(vips.Angle90); err != nil {
			return err
		}
	case 180:
		if err := img.Rotate(vips.Angle180); err != nil {
			return err
		}
	case 270:
		if err := img.Rotate(vips.Angle270); err != nil {
			return err
		}
	}
	if rec.Flip {
		if err := img.Flip(vips.DirectionHorizontal); err != nil {
			return err
		}
	}
	if c := rec.Crop; c != nil && !c.whole() {
		w, h := img.Width(), img.Height()
		left, top := int(math.Round(c.X*float64(w))), int(math.Round(c.Y*float64(h)))
		cw, ch := max(1, min(w-left, int(math.Round(c.W*float64(w))))), max(1, min(h-top, int(math.Round(c.H*float64(h)))))
		if err := img.ExtractArea(left, top, cw, ch); err != nil {
			return err
		}
	}
	if err := toSRGB(img); err != nil { // the light and color below are in 8-bit sRGB
		return err
	}
	if rec.Exposure != 0 || rec.Contrast != 0 || rec.Warmth != 0 {
		// out = a·in + b for each band: exposure scales, contrast pivots on the middle grey,
		// warmth lifts red and lowers blue (an alpha band is left as it is).
		k := 1 + rec.Contrast
		gain := math.Pow(2, rec.Exposure) * k
		off := 128 * (1 - k)
		a := []float64{gain * (1 + 0.12*rec.Warmth), gain, gain * (1 - 0.12*rec.Warmth)}
		b := []float64{off, off, off}
		if img.Bands() == 4 {
			a, b = append(a, 1), append(b, 0)
		}
		if img.Bands() < 3 {
			a, b = []float64{gain}, []float64{off}
		}
		if err := img.Linear(a, b); err != nil {
			return err
		}
		if err := img.Cast(vips.BandFormatUchar); err != nil { // and clipped to 0-255
			return err
		}
	}
	if rec.Saturation != 0 && img.Bands() >= 3 {
		if err := img.Modulate(1, 1+rec.Saturation, 0); err != nil {
			return err
		}
	}
	return nil
}

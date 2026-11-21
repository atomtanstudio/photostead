// Package media makes the derived files: previews, thumbnails, ThumbHash placeholders and video poster frames.
// Originals are only ever read.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/google/uuid"
	"go.n16f.net/thumbhash"
)

const (
	previewSize = 1600 // longest side
	thumbSize   = 640
	hashSize    = 100 // ThumbHash works on images up to 100px
)

// Start initializes libvips. Call once per process.
func Start(concurrency int) error {
	vips.LoggingSettings(func(string, vips.LogLevel, string) {}, vips.LogLevelError)
	return vips.Startup(&vips.Config{ConcurrencyLevel: concurrency, MaxCacheMem: 64 << 20, MaxCacheSize: 100})
}

type Renderer struct {
	DataDir string
}

// Result describes what was rendered. Width and Height are the display shape (after orientation),
// at preview scale, so callers can confirm which way round the original is.
type Result struct {
	ThumbHash     []byte
	Width, Height int
}

// Paths returns where an asset's derived files live: data/derived/<xx>/<yy>/<id>.{thumb,preview}.webp,
// sharded on the random end of the ID (the start of a UUIDv7 is a timestamp).
func (r Renderer) Paths(id uuid.UUID) (thumb, preview string) {
	hex := strings.ReplaceAll(id.String(), "-", "")
	dir := filepath.Join(r.DataDir, "derived", hex[30:32], hex[28:30])
	return filepath.Join(dir, id.String()+".thumb.webp"), filepath.Join(dir, id.String()+".preview.webp")
}

// Image renders a photo from its bytes. libvips decodes it once, shrinking on load, and applies
// EXIF orientation. (Decoding from memory matters on network shares: libvips probes a file
// several times, and each probe there is a round trip.)
func (r Renderer) Image(data []byte, id uuid.UUID) (Result, error) {
	img, err := vips.NewThumbnailWithSizeFromBuffer(data, previewSize, previewSize, vips.InterestingNone, vips.SizeDown)
	if err != nil {
		return Result{}, fmt.Errorf("decode: %w", err)
	}
	return r.finish(img, id)
}

// Video renders a video's poster frame: the first of a few moments in that isn't blank (many
// videos open on black, a fade-in or a plain title card).
func (r Renderer) Video(ctx context.Context, path string, id uuid.UUID, durationMs int) (Result, error) {
	frame, err := posterFrame(ctx, path, durationMs)
	if err != nil {
		return Result{}, err
	}
	img, err := vips.NewThumbnailWithSizeFromBuffer(frame, previewSize, previewSize, vips.InterestingNone, vips.SizeDown)
	if err != nil {
		return Result{}, fmt.Errorf("decode poster frame: %w", err)
	}
	return r.finish(img, id)
}

func (r Renderer) finish(img *vips.ImageRef, id uuid.UUID) (Result, error) {
	defer img.Close()
	if err := toSRGB(img); err != nil {
		return Result{}, err
	}
	res := Result{Width: img.Width(), Height: img.Height()}
	preview, _, err := img.ExportWebp(&vips.WebpExportParams{Quality: 80, StripMetadata: true, ReductionEffort: 4})
	if err != nil {
		return Result{}, fmt.Errorf("encode preview: %w", err)
	}
	// libvips streams the source once, so the smaller sizes are made from the encoded preview
	// rather than by reading the original again.
	thumb, err := vips.NewThumbnailWithSizeFromBuffer(preview, thumbSize, thumbSize, vips.InterestingNone, vips.SizeDown)
	if err != nil {
		return Result{}, fmt.Errorf("thumbnail: %w", err)
	}
	defer thumb.Close()
	thumbBytes, _, err := thumb.ExportWebp(&vips.WebpExportParams{Quality: 75, StripMetadata: true, ReductionEffort: 4})
	if err != nil {
		return Result{}, fmt.Errorf("encode thumbnail: %w", err)
	}
	small, err := vips.NewThumbnailWithSizeFromBuffer(thumbBytes, hashSize, hashSize, vips.InterestingNone, vips.SizeDown)
	if err != nil {
		return Result{}, err
	}
	defer small.Close()
	hashImg, err := small.ToGoImage()
	if err != nil {
		return Result{}, fmt.Errorf("thumbhash input: %w", err)
	}
	res.ThumbHash = thumbhash.EncodeImage(hashImg)

	thumbPath, previewPath := r.Paths(id)
	if err := writeAtomic(previewPath, preview); err != nil {
		return Result{}, err
	}
	if err := writeAtomic(thumbPath, thumbBytes); err != nil {
		return Result{}, err
	}
	return res, nil
}

// toSRGB converts CMYK, 16-bit, Lab and other spaces to 8-bit sRGB for web display.
// Wide-gamut previews come later (decision 2.3); sRGB is the safe default.
func toSRGB(img *vips.ImageRef) error {
	switch img.Interpretation() {
	case vips.InterpretationSRGB, vips.InterpretationBW:
	default:
		if err := img.ToColorSpace(vips.InterpretationSRGB); err != nil {
			return fmt.Errorf("convert to sRGB: %w", err)
		}
	}
	if img.BandFormat() != vips.BandFormatUchar {
		if err := img.Cast(vips.BandFormatUchar); err != nil {
			return err
		}
	}
	return nil
}

// posterTimes are the moments (in seconds) a poster frame is looked for, in order: a second in,
// then a tenth, a quarter and half of the way through. A clip under two seconds is looked at
// halfway, then a quarter and three quarters in.
func posterTimes(durationMs int) []float64 {
	d := float64(durationMs) / 1000
	switch {
	case d <= 0:
		return []float64{1, 5, 15}
	case d < 2:
		return []float64{d / 2, d / 4, d * 3 / 4}
	}
	times := []float64{1}
	for _, f := range []float64{0.1, 0.25, 0.5} {
		if t := d * f; t > times[len(times)-1]+0.5 {
			times = append(times, t)
		}
	}
	return times
}

// posterFrame is the first frame at posterTimes that isn't blank, or the first one found when
// they all are (a video that's dark throughout). Some files can't seek; then it's the first frame.
func posterFrame(ctx context.Context, path string, durationMs int) ([]byte, error) {
	var first []byte
	for _, at := range posterTimes(durationMs) {
		frame, err := frameAt(ctx, path, at)
		if err != nil {
			continue
		}
		if first == nil {
			first = frame
		}
		if !blankFrame(frame) {
			return frame, nil
		}
	}
	if first != nil {
		return first, nil
	}
	return frameAt(ctx, path, 0)
}

// blankFrame judges a frame at ThumbHash size, as look-alikes judge items (see Blank).
func blankFrame(frame []byte) bool {
	small, err := vips.NewThumbnailWithSizeFromBuffer(frame, hashSize, hashSize, vips.InterestingNone, vips.SizeDown)
	if err != nil {
		return false
	}
	defer small.Close()
	if err := toSRGB(small); err != nil {
		return false
	}
	img, err := small.ToGoImage()
	return err == nil && Blank(img)
}

// frameAt takes one frame as PNG, at seconds in (0: the first frame).
func frameAt(ctx context.Context, path string, at float64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if at > 0 {
		// -ss before -i seeks without decoding (and without reading the whole file over the network).
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	}
	args = append(args, "-i", path, "-frames:v", "1", "-an", "-sn", "-dn", "-vf", "scale='min(1920,iw)':-2",
		"-f", "image2pipe", "-c:v", "png", "-")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if out.Len() == 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && err != nil {
			msg = err.Error()
		}
		if msg == "" {
			msg = "no video frame"
		}
		return nil, errors.New("poster frame: " + firstLine(msg))
	}
	return out.Bytes(), nil
}

// Frame is one frame of a video, full size, as a JPEG: a moment kept as a photo. at is how far
// in, in seconds.
func Frame(ctx context.Context, path string, at float64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	}
	args = append(args, "-i", path, "-frames:v", "1", "-an", "-sn", "-dn", "-q:v", "2", "-f", "image2pipe", "-c:v", "mjpeg", "-")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	_ = cmd.Run()
	if out.Len() == 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "no video frame there"
		}
		return nil, errors.New(firstLine(msg))
	}
	return out.Bytes(), nil
}

// Still is one frame of a video at most size pixels across, as a JPEG, for a model to look at. It
// seeks to the keyframe at or before at, without reading the whole file.
func Still(ctx context.Context, path string, at float64, size int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	scale := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", size, size)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-ss", strconv.FormatFloat(at, 'f', 3, 64),
		"-i", path, "-frames:v", "1", "-an", "-sn", "-dn", "-vf", scale, "-q:v", "3", "-f", "image2pipe", "-c:v", "mjpeg", "-"}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	_ = cmd.Run()
	if out.Len() == 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "no video frame there"
		}
		return nil, errors.New(firstLine(msg))
	}
	return out.Bytes(), nil
}

// Trim copies the part of a video from start to end (seconds) into dst, without re-encoding: it
// starts at the keyframe at or before start, so it may begin a moment early.
func Trim(ctx context.Context, src, dst string, start, end float64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	args = append(args, "-i", src, "-t", strconv.FormatFloat(end-start, 'f', 3, 64), "-map", "0:v:0", "-map", "0:a?",
		"-c", "copy", "-map_metadata", "0", "-movflags", "+faststart", "-avoid_negative_ts", "make_zero", dst)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(firstLine(msg))
	}
	return nil
}

// Reason turns a render error into one readable line (libvips errors carry Go stack traces).
func Reason(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "\n\nStack:"); i >= 0 {
		msg = msg[:i]
	}
	return firstLine(strings.TrimSpace(msg))
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// FacePath is where a face's crop is kept: beside its item's renders, so they go together.
func (r Renderer) FacePath(asset, face uuid.UUID) string {
	thumb, _ := r.Paths(asset)
	return filepath.Join(filepath.Dir(thumb), asset.String()+".face-"+face.String()+".webp")
}

// FaceCrops lists an item's face crops (to delete with its renders).
func (r Renderer) FaceCrops(asset uuid.UUID) []string {
	thumb, _ := r.Paths(asset)
	found, _ := filepath.Glob(filepath.Join(filepath.Dir(thumb), asset.String()+".face-*.webp"))
	return found
}

// Face crops a face (or an animal) out of its item's preview: a square around the box (x, y, width,
// height as fractions of the picture), margin times its longer side, size pixels across. It's kept
// for next time.
func (r Renderer) Face(asset, face uuid.UUID, box []float32, size int, margin float64) (string, error) {
	path := r.FacePath(asset, face)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if len(box) != 4 {
		return "", fmt.Errorf("face box: %d numbers", len(box))
	}
	_, preview := r.Paths(asset)
	img, err := vips.NewImageFromFile(preview)
	if err != nil {
		return "", fmt.Errorf("read preview: %w", err)
	}
	defer img.Close()
	W, H := float64(img.Width()), float64(img.Height())
	cx, cy := (float64(box[0])+float64(box[2])/2)*W, (float64(box[1])+float64(box[3])/2)*H
	side := min(max(float64(box[2])*W, float64(box[3])*H)*margin, W, H)
	left := min(max(cx-side/2, 0), W-side)
	top := min(max(cy-side/2, 0), H-side)
	if err := img.ExtractArea(int(left), int(top), max(1, int(side)), max(1, int(side))); err != nil {
		return "", fmt.Errorf("crop: %w", err)
	}
	if err := img.Thumbnail(size, size, vips.InterestingNone); err != nil {
		return "", fmt.Errorf("resize: %w", err)
	}
	data, _, err := img.ExportWebp(&vips.WebpExportParams{Quality: 82, StripMetadata: true, ReductionEffort: 4})
	if err != nil {
		return "", fmt.Errorf("encode: %w", err)
	}
	return path, writeAtomic(path, data)
}

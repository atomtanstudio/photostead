package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CopyRecipe names how playable copies are made: H.264 High, 8-bit 4:2:0 SDR, at most 1920 px on
// the long side, AAC stereo, MP4 with the index first. A new recipe makes every copy again.
const CopyRecipe = "h264-1920-sdr-v1"

// VideoInfo is what ffprobe says about a video, as far as playing it in a browser goes.
type VideoInfo struct {
	Container string // ffprobe's format name, e.g. "mov,mp4,m4a,3gp,3g2,mj2" or "matroska,webm"
	Codec     string // the first video stream's codec: h264, hevc, vp9, av1, mpeg4, prores…
	Profile   string
	Level     int
	PixFmt    string
	BitDepth  int
	Transfer  string // color transfer: smpte2084 (PQ) and arib-std-b67 (HLG) are HDR
	DolbyOnly bool   // Dolby Vision without a base layer others can show
	HDR       bool
	Audio     string // the first audio stream's codec, or ""
	Width     int
	Height    int
}

// Probe reads a video's streams (headers only, so it's quick even over a network share).
func Probe(ctx context.Context, path string) (VideoInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", "--", path)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		msg := firstLine(strings.TrimSpace(stderr.String()))
		if msg == "" {
			msg = err.Error()
		}
		return VideoInfo{}, errors.New(msg)
	}
	var p struct {
		Format struct {
			Name string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			Type        string `json:"codec_type"`
			Codec       string `json:"codec_name"`
			Tag         string `json:"codec_tag_string"`
			Profile     string `json:"profile"`
			Level       int    `json:"level"`
			PixFmt      string `json:"pix_fmt"`
			Bits        string `json:"bits_per_raw_sample"`
			Transfer    string `json:"color_transfer"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
			SideData []struct {
				Type   string `json:"side_data_type"`
				Compat int    `json:"dv_bl_signal_compatibility_id"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		return VideoInfo{}, fmt.Errorf("ffprobe's answer: %w", err)
	}
	v := VideoInfo{Container: p.Format.Name}
	for _, s := range p.Streams {
		switch {
		case s.Type == "video" && v.Codec == "" && s.Disposition.AttachedPic == 0:
			v.Codec, v.Profile, v.Level, v.PixFmt, v.Transfer = s.Codec, s.Profile, s.Level, s.PixFmt, s.Transfer
			v.Width, v.Height = s.Width, s.Height
			v.BitDepth = bitDepth(s.PixFmt, s.Bits)
			for _, d := range s.SideData {
				if d.Type == "DOVI configuration record" && d.Compat == 0 {
					v.DolbyOnly = true
				}
			}
		case s.Type == "audio" && v.Audio == "":
			v.Audio = s.Codec
		}
	}
	if v.Codec == "" {
		return v, errors.New("no video stream")
	}
	v.HDR = v.Transfer == "smpte2084" || v.Transfer == "arib-std-b67" || v.DolbyOnly
	return v, nil
}

// bitDepth is a stream's bits per sample, from ffprobe's count or its pixel format's name.
func bitDepth(pixFmt, bits string) int {
	if n, err := strconv.Atoi(bits); err == nil && n > 0 {
		return n
	}
	for _, d := range []string{"16", "12", "10"} {
		if strings.Contains(pixFmt, "p"+d) {
			n, _ := strconv.Atoi(d)
			return n
		}
	}
	return 8
}

// Direct says whether every browser plays the video as it is: H.264, 8-bit 4:2:0, SDR, in MP4 or
// QuickTime, with AAC or MP3 sound (or none). Anything else gets a copy.
func (v VideoInfo) Direct() bool {
	mp4 := strings.Contains(v.Container, "mp4") || strings.Contains(v.Container, "mov")
	audio := v.Audio == "" || v.Audio == "aac" || v.Audio == "mp3"
	return mp4 && v.Codec == "h264" && (v.PixFmt == "yuv420p" || v.PixFmt == "yuvj420p") && !v.HDR && audio
}

// Codecs is the video's codec as a browser's canPlayType wants it (RFC 6381), when there's a name
// for it: with it, a browser that plays the original (Safari and HEVC, say) gets the original.
func (v VideoInfo) Codecs() string {
	switch v.Codec {
	case "h264":
		profile := map[string]string{"Baseline": "42", "Constrained Baseline": "42", "Main": "4d", "High": "64", "High 10": "6e"}[v.Profile]
		if profile == "" {
			profile = "64"
		}
		return fmt.Sprintf("avc1.%s00%02x", profile, max(v.Level, 30))
	case "hevc":
		if v.BitDepth > 8 {
			return fmt.Sprintf("hvc1.2.4.L%d.B0", max(v.Level, 93))
		}
		return fmt.Sprintf("hvc1.1.6.L%d.B0", max(v.Level, 93))
	case "vp9":
		return fmt.Sprintf("vp09.00.10.%02d", v.BitDepth)
	case "av1":
		return fmt.Sprintf("av01.0.08M.%02d", v.BitDepth)
	case "vp8":
		return "vp8"
	}
	return ""
}

// PlayType is the original's type with its codecs, for a browser's canPlayType (e.g. video/mp4;
// codecs="hvc1.2.4.L150.B0, mp4a.40.2"), or "" when there's no standard name for one of them
// (PCM sound, say), so the copy is played.
func (v VideoInfo) PlayType() string {
	c := v.Codecs()
	if c == "" {
		return ""
	}
	if v.Audio != "" {
		a := map[string]string{"aac": "mp4a.40.2", "mp3": "mp4a.6b", "ac3": "ac-3", "eac3": "ec-3", "opus": "opus", "flac": "flac",
			"alac": "alac", "vorbis": "vorbis"}[v.Audio]
		if a == "" {
			return ""
		}
		c += ", " + a
	}
	mime := "video/mp4"
	if strings.Contains(v.Container, "webm") || strings.Contains(v.Container, "matroska") {
		mime = "video/webm"
	}
	return fmt.Sprintf("%s; codecs=\"%s\"", mime, c)
}

// PlayPath is where an item's playable copy is kept, beside its thumbnail and preview.
func (r Renderer) PlayPath(id uuid.UUID) string {
	thumb, _ := r.Paths(id)
	return filepath.Join(filepath.Dir(thumb), id.String()+".play.mp4")
}

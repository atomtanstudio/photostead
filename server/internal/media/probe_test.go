package media

import "testing"

func TestPlayback(t *testing.T) {
	mp4 := "mov,mp4,m4a,3gp,3g2,mj2"
	for _, c := range []struct {
		name   string
		v      VideoInfo
		direct bool
		typ    string
	}{
		{"phone H.264", VideoInfo{Container: mp4, Codec: "h264", Profile: "High", Level: 40, PixFmt: "yuv420p", Audio: "aac"}, true,
			`video/mp4; codecs="avc1.640028, mp4a.40.2"`},
		{"silent clip", VideoInfo{Container: mp4, Codec: "h264", Profile: "Main", Level: 31, PixFmt: "yuv420p"}, true, `video/mp4; codecs="avc1.4d001f"`},
		{"iPhone HDR", VideoInfo{Container: mp4, Codec: "hevc", Level: 150, PixFmt: "yuv420p10le", BitDepth: 10, Transfer: "arib-std-b67", HDR: true, Audio: "aac"},
			false, `video/mp4; codecs="hvc1.2.4.L150.B0, mp4a.40.2"`},
		{"camera sound", VideoInfo{Container: mp4, Codec: "h264", Level: 40, PixFmt: "yuv420p", Audio: "ac3"}, false, `video/mp4; codecs="avc1.640028, ac-3"`},
		{"PCM sound", VideoInfo{Container: mp4, Codec: "h264", Level: 40, PixFmt: "yuv420p", Audio: "pcm_s16le"}, false, ""},
		{"old MPEG-4", VideoInfo{Container: mp4, Codec: "mpeg4", PixFmt: "yuv420p", Audio: "aac"}, false, ""},
		{"WebM", VideoInfo{Container: "matroska,webm", Codec: "vp9", PixFmt: "yuv420p", BitDepth: 8, Audio: "opus"}, false, `video/webm; codecs="vp09.00.10.08, opus"`},
		{"4:2:2", VideoInfo{Container: mp4, Codec: "h264", PixFmt: "yuv422p", Audio: "aac"}, false, `video/mp4; codecs="avc1.64001e, mp4a.40.2"`},
	} {
		if got := c.v.Direct(); got != c.direct {
			t.Errorf("%s: Direct() = %v", c.name, got)
		}
		if got := c.v.PlayType(); got != c.typ {
			t.Errorf("%s: PlayType() = %s, want %s", c.name, got, c.typ)
		}
	}
}

func TestBitDepth(t *testing.T) {
	for in, want := range map[[2]string]int{{"yuv420p", ""}: 8, {"yuv420p10le", ""}: 10, {"yuv422p12be", ""}: 12, {"yuv420p", "10"}: 10} {
		if got := bitDepth(in[0], in[1]); got != want {
			t.Errorf("bitDepth(%v) = %d, want %d", in, got, want)
		}
	}
}

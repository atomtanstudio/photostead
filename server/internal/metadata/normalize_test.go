package metadata

import (
	"strings"
	"testing"
	"time"
)

var (
	la, _ = time.LoadLocation("America/Los_Angeles")
	now   = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	mtime = time.Date(2025, 7, 17, 17, 0, 40, 0, time.UTC)
)

func TestDates(t *testing.T) {
	cases := []struct {
		name       string
		tags       Tags
		wantSource string
		wantDate   string // local day
		wantUTC    string // instant, RFC 3339 in UTC
		wantOffset *int16
	}{
		{
			name:       "exif with offset (phone photo in Tokyo)",
			tags:       Tags{"ExifIFD:DateTimeOriginal": "2024:05:01 23:30:00", "ExifIFD:OffsetTimeOriginal": "+09:00"},
			wantSource: "exif-original", wantDate: "2024-05-01", wantUTC: "2024-05-01T14:30:00Z", wantOffset: i16(540),
		},
		{
			name:       "subsec composite wins and carries the offset",
			tags:       Tags{"Composite:SubSecDateTimeOriginal": "2024:05:01 23:30:00.250-04:00", "ExifIFD:DateTimeOriginal": "2024:05:01 23:30:00"},
			wantSource: "exif-original", wantDate: "2024-05-01", wantUTC: "2024-05-02T03:30:00.25Z", wantOffset: i16(-240),
		},
		{
			name:       "exif without offset keeps the camera's day",
			tags:       Tags{"ExifIFD:DateTimeOriginal": "2007:10:06 23:50:25"},
			wantSource: "exif-original", wantDate: "2007-10-06", wantUTC: "2007-10-07T06:50:25Z",
		},
		{
			name:       "zero exif date falls through to the file date",
			tags:       Tags{"ExifIFD:DateTimeOriginal": "0000:00:00 00:00:00", "System:FileModifyDate": "2025:07:17 10:00:40-07:00"},
			wantSource: "file-modified", wantDate: "2025-07-17", wantUTC: "2025-07-17T17:00:40Z",
		},
		{
			name:       "quicktime is UTC: local day comes from the server zone",
			tags:       Tags{"QuickTime:CreateDate": "2026:05:07 02:58:05", "System:FileModifyDate": "2026:05:06 19:58:36-07:00"},
			wantSource: "quicktime", wantDate: "2026-05-06", wantUTC: "2026-05-07T02:58:05Z",
		},
		{
			name:       "apple keys date beats quicktime and keeps the offset",
			tags:       Tags{"Keys:CreationDate": "2026:05:06 13:58:05-07:00", "QuickTime:CreateDate": "2026:05:06 20:58:05"},
			wantSource: "apple-creation", wantDate: "2026-05-06", wantUTC: "2026-05-06T20:58:05Z", wantOffset: i16(-420),
		},
		{
			name:       "matroska Z is a UTC instant, not a place",
			tags:       Tags{"Info:DateTimeOriginal": "2021:11:15 15:33:25Z"},
			wantSource: "matroska", wantDate: "2021-11-15", wantUTC: "2021-11-15T15:33:25Z",
		},
		{
			name:       "quicktime zero date is ignored",
			tags:       Tags{"QuickTime:CreateDate": "0000:00:00 00:00:00", "System:FileModifyDate": "2026:05:24 06:45:52-07:00"},
			wantSource: "file-modified", wantDate: "2026-05-24", wantUTC: "2026-05-24T13:45:52Z",
		},
		{
			name:       "QuickTime's zero date (1904) means unset",
			tags:       Tags{"QuickTime:CreateDate": "1904:01:01 00:00:00", "System:FileModifyDate": "2025:07:17 10:00:40-07:00"},
			wantSource: "file-modified", wantDate: "2025-07-17", wantUTC: "2025-07-17T17:00:40Z",
		},
		{
			name:       "Unix zero date (1970) means unset",
			tags:       Tags{"ExifIFD:DateTimeOriginal": "1970:01:01 00:00:00", "System:FileModifyDate": "2025:07:17 10:00:40-07:00"},
			wantSource: "file-modified", wantDate: "2025-07-17", wantUTC: "2025-07-17T17:00:40Z",
		},
		{
			name:       "xmp date-only means midday that day",
			tags:       Tags{"XMP-photoshop:DateCreated": "2019-05-01"},
			wantSource: "xmp-created", wantDate: "2019-05-01", wantUTC: "2019-05-01T19:00:00Z",
		},
		{
			name:       "future date is rejected",
			tags:       Tags{"ExifIFD:DateTimeOriginal": "2099:01:01 00:00:00", "System:FileModifyDate": "2025:07:17 10:00:40-07:00"},
			wantSource: "file-modified", wantDate: "2025-07-17", wantUTC: "2025-07-17T17:00:40Z",
		},
		{
			name:       "file date read by a server running in UTC still lands on the owner's local day",
			tags:       Tags{"System:FileModifyDate": "2026:07:15 00:09:34+00:00"},
			wantSource: "file-modified", wantDate: "2026-07-14", wantUTC: "2026-07-15T00:09:34Z",
		},
		{
			name:       "nothing at all: the file's modification time",
			tags:       Tags{},
			wantSource: "file-modified", wantDate: "2025-07-17", wantUTC: "2025-07-17T17:00:40Z",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Normalize(c.tags, la, mtime, now)
			if got.DateSource != c.wantSource {
				t.Errorf("source = %q, want %q", got.DateSource, c.wantSource)
			}
			if d := got.LocalDate.Format("2006-01-02"); d != c.wantDate {
				t.Errorf("local date = %s, want %s", d, c.wantDate)
			}
			if u := got.TakenAt.UTC().Format(time.RFC3339Nano); u != c.wantUTC {
				t.Errorf("taken at = %s, want %s", u, c.wantUTC)
			}
			switch {
			case c.wantOffset == nil && got.OffsetMin != nil:
				t.Errorf("offset = %d, want none", *got.OffsetMin)
			case c.wantOffset != nil && (got.OffsetMin == nil || *got.OffsetMin != *c.wantOffset):
				t.Errorf("offset = %v, want %d", got.OffsetMin, *c.wantOffset)
			}
		})
	}
}

func TestOrientationAndDuration(t *testing.T) {
	info := Normalize(Tags{"Composite:ImageSize": "4032 3024", "IFD0:Orientation": float64(6)}, la, mtime, now)
	if w, h := info.Oriented(); w != 3024 || h != 4032 {
		t.Errorf("oriented = %dx%d, want 3024x4032", w, h)
	}
	v := Normalize(Tags{"Composite:ImageSize": "1920 1080", "Composite:Rotation": float64(-90), "QuickTime:Duration": 10.1006}, la, mtime, now)
	if w, h := v.Oriented(); w != 1080 || h != 1920 {
		t.Errorf("rotated video = %dx%d, want 1080x1920", w, h)
	}
	if v.DurationMs != 10101 {
		t.Errorf("duration = %d ms, want 10101", v.DurationMs)
	}
	mkv := Normalize(Tags{"Info:Duration": "00:05:48.882000000"}, la, mtime, now)
	if mkv.DurationMs != 348882 {
		t.Errorf("mkv duration = %d ms, want 348882", mkv.DurationMs)
	}
}

func TestRawJSONDropsHugeValuesAndNULs(t *testing.T) {
	big := make([]byte, maxRawValue+1)
	for i := range big {
		big[i] = 'a'
	}
	b, err := RawJSON(Tags{"PNG:Workflow": string(big), "IFD0:Make": "Can\x00on", "SourceFile": "/x"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if want := `"IFD0:Make":"Canon"`; !strings.Contains(s, want) {
		t.Errorf("raw = %s, missing %s", s, want)
	}
	if !strings.Contains(s, `"truncated":true`) || strings.Contains(s, "SourceFile") {
		t.Errorf("raw = %s", s)
	}
}

func i16(v int16) *int16 { return &v }

func TestDateBasis(t *testing.T) {
	for tags, want := range map[string]Tags{
		"recorded_offset": {"ExifIFD:DateTimeOriginal": "2024:05:01 23:30:00", "ExifIFD:OffsetTimeOriginal": "+09:00"},
		"camera_clock":    {"ExifIFD:DateTimeOriginal": "2007:10:06 23:50:25"},
		"instant":         {"QuickTime:CreateDate": "2026:05:07 02:58:05"},
	} {
		if got := Normalize(want, la, mtime, now).DateBasis; got != tags {
			t.Errorf("%v: basis = %s, want %s", want, got, tags)
		}
	}
	if got := Normalize(Tags{}, la, mtime, now).DateBasis; got != "instant" {
		t.Errorf("file time basis = %s, want instant", got)
	}
}

// Stars, rejects and color labels written by Lightroom, Bridge or a camera.
func TestRatingsAndLabels(t *testing.T) {
	for _, c := range []struct {
		tags   Tags
		rating int
		label  string
	}{
		{Tags{"XMP-xmp:Rating": 4.0, "XMP-xmp:Label": "Green"}, 4, "green"},
		{Tags{"IFD0:Rating": 5.0}, 5, ""},
		{Tags{"XMP-xmp:Rating": -1.0}, -1, ""},                   // a reject
		{Tags{"XMP-xmp:Rating": 9.0}, 0, ""},                     // not a rating
		{Tags{"XMP-xmp:Label": "To Do"}, 0, ""},                  // a custom label: not one of the five colors
		{Tags{"XMP-xmp:Rating": "3", "IFD0:Rating": 1.0}, 3, ""}, // XMP first
	} {
		info := Normalize(c.tags, time.UTC, time.Now(), time.Now())
		if info.Rating != c.rating || info.Label != c.label {
			t.Errorf("%v: rating %d label %q, want %d %q", c.tags, info.Rating, info.Label, c.rating, c.label)
		}
	}
}

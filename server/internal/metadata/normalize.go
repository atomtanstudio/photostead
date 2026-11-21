package metadata

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Info is what Photostead indexes from a file's metadata.
type Info struct {
	TakenAt    time.Time
	OffsetMin  *int16 // the UTC offset recorded with the capture time; nil when the file doesn't say
	LocalDate  time.Time
	DateSource string
	DateBasis  string // recorded_offset, camera_clock or instant (see migration 00002)

	MIME        string
	Width       int // as stored, before orientation; see Oriented
	Height      int
	Orientation int
	Rotation    int // video rotation in degrees
	DurationMs  int

	Make, Model, Lens string
	FocalMM           *float64
	FNumber           *float64
	ExposureS         *float64
	ISO               *int
	Lat, Lon, Alt     *float64
	Title             string
	Description       string

	// Apple writes the same identifier into a Live Photo's still and its motion clip.
	ContentIdentifier string

	// Stars (1-5, or -1 for a reject, as Lightroom and Bridge write it) and a color label, when the
	// file carries them.
	Rating int
	Label  string // red, yellow, green, blue or purple
}

// Oriented returns the display width and height, after EXIF orientation or video rotation.
func (i Info) Oriented() (int, int) {
	if i.Orientation >= 5 && i.Orientation <= 8 || i.Rotation == 90 || i.Rotation == 270 {
		return i.Height, i.Width
	}
	return i.Width, i.Height
}

type clock int

const (
	wallClock clock = iota // local time as the camera showed it; offset only if the file records one
	instant                // a moment in time (QuickTime and Matroska UTC clocks, file dates); the local
	//                        day comes from the owner's time zone, never from UTC or the server's clock
)

// Date sources, most trusted first. The first one that parses wins.
var dateTags = []struct {
	tag, offsetTag, source string
	clock                  clock
}{
	{"Composite:SubSecDateTimeOriginal", "", "exif-original", wallClock},
	{"ExifIFD:DateTimeOriginal", "ExifIFD:OffsetTimeOriginal", "exif-original", wallClock},
	{"XMP-exif:DateTimeOriginal", "", "xmp-original", wallClock},
	{"Keys:CreationDate", "", "apple-creation", wallClock},
	{"XMP-photoshop:DateCreated", "", "xmp-created", wallClock},
	{"Composite:SubSecCreateDate", "", "exif-digitized", wallClock},
	{"ExifIFD:CreateDate", "ExifIFD:OffsetTimeDigitized", "exif-digitized", wallClock},
	{"XMP-xmp:CreateDate", "", "xmp-create", wallClock},
	{"Composite:DateTimeCreated", "", "iptc", wallClock},
	{"Info:DateTimeOriginal", "", "matroska", instant},
	{"QuickTime:CreateDate", "", "quicktime", instant},
	{"PNG:CreationTime", "", "png", wallClock},
	// The offset exiftool prints on a file date is just the zone of the process reading it.
	{"System:FileModifyDate", "", "file-modified", instant},
}

// Normalize extracts Info from exiftool tags. zone is the owner's time zone: it gives the local
// day for instants and the meaning of camera clocks without an offset. fileMtime is the last
// resort when no tag has a usable date.
func Normalize(t Tags, zone *time.Location, fileMtime time.Time, now time.Time) Info {
	var info Info
	info.MIME = str(t, "File:MIMEType")

	info.DateSource, info.DateBasis = "file-modified", "instant"
	info.TakenAt = fileMtime
	info.LocalDate = civil(fileMtime.In(zone))
	for _, d := range dateTags {
		s := str(t, d.tag)
		if s == "" {
			continue
		}
		ts, ok := parseTime(s)
		if !ok || !plausible(ts, now) {
			continue
		}
		if ts.offset == nil && d.offsetTag != "" {
			ts.offset = parseOffset(str(t, d.offsetTag))
		}
		info.DateSource = d.source
		switch {
		case d.clock == instant:
			// A moment in time: its own offset (if any) says nothing about where it was taken.
			loc := time.UTC
			if ts.offset != nil {
				loc = time.FixedZone("", int(*ts.offset)*60)
			}
			info.TakenAt = ts.wall(loc)
			info.LocalDate = civil(info.TakenAt.In(zone))
			info.DateBasis = "instant"
		case ts.offset != nil:
			info.TakenAt = ts.wall(time.FixedZone("", int(*ts.offset)*60))
			info.LocalDate = ts.date()
			info.OffsetMin = ts.offset
			info.DateBasis = "recorded_offset"
		default:
			info.TakenAt = ts.wall(zone)
			info.LocalDate = ts.date()
			info.DateBasis = "camera_clock"
		}
		break
	}

	if w, h, ok := pair(str(t, "Composite:ImageSize")); ok {
		info.Width, info.Height = w, h
	}
	info.Orientation = intOf(t, "IFD0:Orientation")
	info.Rotation = ((intOf(t, "Composite:Rotation") % 360) + 360) % 360
	for _, k := range []string{"QuickTime:Duration", "Info:Duration", "Composite:Duration", "ASF:PlayDuration", "RIFF:Duration"} {
		if s, ok := durationSeconds(t[k]); ok {
			info.DurationMs = int(math.Round(s * 1000))
			break
		}
	}

	info.Make = first(t, "IFD0:Make", "Keys:Make", "UserData:Make", "QuickTime:Make")
	info.Model = first(t, "IFD0:Model", "Keys:Model", "UserData:Model", "QuickTime:Model")
	info.Lens = first(t, "ExifIFD:LensModel", "Composite:LensID")
	info.FocalMM = floatPtr(t, "ExifIFD:FocalLength")
	info.FNumber = floatPtr(t, "ExifIFD:FNumber")
	info.ExposureS = floatPtr(t, "ExifIFD:ExposureTime")
	if v := intOf(t, "ExifIFD:ISO"); v > 0 {
		info.ISO = &v
	}
	info.Lat, info.Lon, info.Alt = floatPtr(t, "Composite:GPSLatitude"), floatPtr(t, "Composite:GPSLongitude"), floatPtr(t, "Composite:GPSAltitude")
	if info.Lat == nil {
		if f := strings.Fields(str(t, "Keys:GPSCoordinates")); len(f) >= 2 {
			lat, e1 := strconv.ParseFloat(f[0], 64)
			lon, e2 := strconv.ParseFloat(f[1], 64)
			if e1 == nil && e2 == nil {
				info.Lat, info.Lon = &lat, &lon
			}
		}
	}
	if info.Lat != nil && *info.Lat == 0 && info.Lon != nil && *info.Lon == 0 {
		info.Lat, info.Lon, info.Alt = nil, nil, nil // "null island" means no fix
	}
	info.ContentIdentifier = first(t, "Apple:ContentIdentifier", "Keys:ContentIdentifier", "QuickTime:ContentIdentifier")
	info.Title = first(t, "XMP-dc:Title", "IPTC:ObjectName")
	info.Description = first(t, "XMP-dc:Description", "IFD0:ImageDescription", "IPTC:Caption-Abstract", "Keys:Description")
	for _, k := range []string{"XMP-xmp:Rating", "IFD0:Rating"} {
		if _, ok := t[k]; ok {
			if r := intOf(t, k); r >= -1 && r <= 5 {
				info.Rating = r
				break
			}
		}
	}
	switch l := strings.ToLower(str(t, "XMP-xmp:Label")); l {
	case "red", "yellow", "green", "blue", "purple":
		info.Label = l
	}
	return info
}

type parsed struct {
	y, mo, d, h, mi, s, ns int
	offset                 *int16
	utc                    bool // the string ended in Z
}

func (p parsed) wall(loc *time.Location) time.Time {
	return time.Date(p.y, time.Month(p.mo), p.d, p.h, p.mi, p.s, p.ns, loc)
}

func (p parsed) date() time.Time { return time.Date(p.y, time.Month(p.mo), p.d, 0, 0, 0, 0, time.UTC) }

// 2024:05:01 12:34:56.789+02:00, 2024-05-01T12:34:56Z, 2024:05:01, 2024-05-01 12:34 …
var timeRe = regexp.MustCompile(`^(\d{4})[:\-](\d{2})[:\-](\d{2})(?:[ T](\d{2}):(\d{2})(?::(\d{2}))?(?:[.,](\d{1,9}))?)?\s*(Z|[+\-]\d{2}:?\d{2})?`)

func parseTime(s string) (parsed, bool) {
	m := timeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return parsed{}, false
	}
	n := func(x string) int { v, _ := strconv.Atoi(x); return v }
	p := parsed{y: n(m[1]), mo: n(m[2]), d: n(m[3]), h: n(m[4]), mi: n(m[5]), s: n(m[6])}
	if m[4] == "" {
		p.h = 12 // a date with no time: midday keeps it on the same day in any nearby zone
	}
	if m[7] != "" {
		frac := (m[7] + "000000000")[:9]
		p.ns = n(frac)
	}
	if p.mo < 1 || p.mo > 12 || p.d < 1 || p.d > 31 || p.h > 23 || p.mi > 59 || p.s > 60 {
		return parsed{}, false
	}
	switch z := m[8]; {
	case z == "Z":
		zero := int16(0)
		p.offset, p.utc = &zero, true
	case z != "":
		p.offset = parseOffset(z)
	}
	return p, true
}

func parseOffset(s string) *int16 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ":", "")
	if len(s) != 5 || (s[0] != '+' && s[0] != '-') {
		return nil
	}
	h, e1 := strconv.Atoi(s[1:3])
	m, e2 := strconv.Atoi(s[3:5])
	if e1 != nil || e2 != nil || h > 14 || m > 59 {
		return nil
	}
	v := int16(h*60 + m)
	if s[0] == '-' {
		v = -v
	}
	return &v
}

// plausible rejects zero dates, dates before photography, and dates in the future. QuickTime's
// and Unix's zero moments (1904-01-01 and 1970-01-01 at midnight) mean "not set".
func plausible(p parsed, now time.Time) bool {
	if p.y < 1826 {
		return false
	}
	if (p.y == 1904 || p.y == 1970) && p.mo == 1 && p.d == 1 && p.h == 0 && p.mi == 0 && p.s == 0 {
		return false
	}
	return p.wall(time.UTC).Before(now.Add(48 * time.Hour))
}

func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func str(t Tags, k string) string {
	switch v := t[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func first(t Tags, keys ...string) string {
	for _, k := range keys {
		if s := str(t, k); s != "" {
			return s
		}
	}
	return ""
}

func intOf(t Tags, k string) int {
	switch v := t[k].(type) {
	case float64:
		return int(v)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(v))
		return i
	}
	return 0
}

func floatPtr(t Tags, k string) *float64 {
	switch v := t[k].(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return &v
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return &f
		}
	}
	return nil
}

func pair(s string) (int, int, bool) {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == 'x' })
	if len(f) != 2 {
		return 0, 0, false
	}
	w, e1 := strconv.Atoi(f[0])
	h, e2 := strconv.Atoi(f[1])
	return w, h, e1 == nil && e2 == nil && w > 0 && h > 0
}

// durationSeconds accepts exiftool's numeric seconds or "HH:MM:SS.fff".
func durationSeconds(v any) (float64, bool) {
	switch d := v.(type) {
	case float64:
		return d, d > 0
	case string:
		d = strings.TrimSpace(strings.TrimSuffix(d, " s"))
		if f, err := strconv.ParseFloat(d, 64); err == nil {
			return f, f > 0
		}
		parts := strings.Split(d, ":")
		if len(parts) == 3 {
			h, e1 := strconv.ParseFloat(parts[0], 64)
			m, e2 := strconv.ParseFloat(parts[1], 64)
			s, e3 := strconv.ParseFloat(parts[2], 64)
			if e1 == nil && e2 == nil && e3 == nil {
				total := h*3600 + m*60 + s
				return total, total > 0
			}
		}
	}
	return 0, false
}

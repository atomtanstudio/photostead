package metadata

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NameDate is a date written into a file or folder name, such as "IMG_20230101_123456.jpg",
// "Krea2_2026-07-15-000933.png" or ".../txt2img-images/2023-09-12/00114.png".
type NameDate struct {
	Year, Month, Day     int
	Hour, Minute, Second int
	HasTime              bool
}

// Date returns the day at midnight UTC (a calendar day, like assets.local_date).
func (n NameDate) Date() time.Time {
	return time.Date(n.Year, time.Month(n.Month), n.Day, 0, 0, 0, 0, time.UTC)
}

// In returns the moment the name describes, read as a wall clock in zone. Without a time it's
// midday, which keeps the same day in any nearby zone.
func (n NameDate) In(zone *time.Location) time.Time {
	if !n.HasTime {
		return time.Date(n.Year, time.Month(n.Month), n.Day, 12, 0, 0, 0, zone)
	}
	return time.Date(n.Year, time.Month(n.Month), n.Day, n.Hour, n.Minute, n.Second, 0, zone)
}

var (
	// 2023-09-12, 2023_09_12, 2023.09.12, optionally followed by a time: -000933, _11_21,
	// T10:10:10, " at 10.11.12" (macOS screenshots) …
	separated = regexp.MustCompile(`(?:^|[^0-9])(20[0-3][0-9])[-_.](0[1-9]|1[0-2])[-_.](0[1-9]|[12][0-9]|3[01])(?:(?:[-_ T.]| at )((?:[01][0-9]|2[0-3])[-_.:]?[0-5][0-9](?:[-_.:]?[0-5][0-9])?))?(?:[^0-9]|$)`)
	// 20230912, optionally followed by a time: _123456, -004435, T101010. The neighbours mustn't be
	// hex digits, so runs inside hashes and UUIDs don't count.
	compact = regexp.MustCompile(`(?:^|[^0-9A-Fa-f])(20[0-3][0-9])(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])(?:[-_ T]?((?:[01][0-9]|2[0-3])[0-5][0-9](?:[0-5][0-9])?))?(?:[^0-9A-Fa-f]|$)`)
)

// ParseNameDate finds a date in a relative path: in the file name first, then in the folders,
// nearest first. It rejects impossible dates and dates after now.
func ParseNameDate(rel string, now time.Time) (NameDate, bool) {
	parts := strings.Split(path.Clean(rel), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		name := parts[i]
		if i == len(parts)-1 {
			name = strings.TrimSuffix(name, path.Ext(name))
		}
		for _, re := range []*regexp.Regexp{separated, compact} {
			for _, m := range re.FindAllStringSubmatch(name, -1) {
				if nd, ok := build(m, now); ok {
					return nd, true
				}
			}
		}
	}
	return NameDate{}, false
}

func build(m []string, now time.Time) (NameDate, bool) {
	atoi := func(s string) int { v, _ := strconv.Atoi(s); return v }
	nd := NameDate{Year: atoi(m[1]), Month: atoi(m[2]), Day: atoi(m[3])}
	if t := time.Date(nd.Year, time.Month(nd.Month), nd.Day, 0, 0, 0, 0, time.UTC); t.Day() != nd.Day || t.After(now.Add(48*time.Hour)) {
		return NameDate{}, false // 2023-02-30, or the future
	}
	if clock := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, m[4]); len(clock) >= 4 {
		nd.Hour, nd.Minute, nd.HasTime = atoi(clock[0:2]), atoi(clock[2:4]), true
		if len(clock) >= 6 {
			nd.Second = atoi(clock[4:6])
		}
	}
	return nd, true
}

// CopyTime is one copy of an item: where it is and when it was last modified.
type CopyTime struct {
	Rel   string
	Mtime time.Time
}

// Derived is the date of an item with no date inside its file, worked out from its copies.
type Derived struct {
	TakenAt   time.Time
	LocalDate time.Time
	Source    string // file-modified or name
	Basis     string // instant (file time) or camera_clock (a name's wall clock)
}

// DateFromCopies dates an item that has no date inside its file (decision 1.7): the earliest
// file time among its copies, unless a copy's file or folder name holds a date that is earlier
// than that copy's own file time (a file can't be made after it was saved), in which case the
// earliest such name date wins. Names are read as wall clocks in the owner's zone.
func DateFromCopies(copies []CopyTime, zone *time.Location, now time.Time) Derived {
	var d Derived
	for _, c := range copies {
		if d.TakenAt.IsZero() || c.Mtime.Before(d.TakenAt) {
			d.TakenAt = c.Mtime
		}
	}
	d.LocalDate, d.Source, d.Basis = civil(d.TakenAt.In(zone)), "file-modified", "instant"
	var best *NameDate
	for _, c := range copies {
		nd, ok := ParseNameDate(c.Rel, now)
		if !ok || !nd.Date().Before(civil(c.Mtime.In(zone))) {
			continue
		}
		if best == nil || nd.Date().Before(best.Date()) || (nd.Date().Equal(best.Date()) && nd.HasTime && !best.HasTime) {
			n := nd
			best = &n
		}
	}
	if best != nil && best.Date().Before(d.LocalDate) {
		d.TakenAt, d.LocalDate, d.Source, d.Basis = best.In(zone), best.Date(), "name", "camera_clock"
	}
	return d
}

package move

import (
	"encoding/csv"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Summary is what a person reviews before anything is copied.
type Summary struct {
	Person          string      `json:"person"`
	CreatedAt       time.Time   `json:"createdAt"`
	Items           int         `json:"items"`
	Photos          int         `json:"photos"`
	Videos          int         `json:"videos"`
	PhotoBytes      int64       `json:"photoBytes"`
	VideoBytes      int64       `json:"videoBytes"`
	LivePhotos      int         `json:"livePhotos"`
	Stacked         int         `json:"stacked"`
	Sidecars        int         `json:"sidecars"`
	SidecarBytes    int64       `json:"sidecarBytes"`
	SidecarsLeft    []Count     `json:"sidecarsLeft"`
	SpareSidecars   int         `json:"spareSidecars"`
	CopiesLeft      int         `json:"copiesLeft"`
	CopiesLeftBytes int64       `json:"copiesLeftBytes"`
	Renamed         int         `json:"renamed"`
	RenamedExamples []string    `json:"renamedExamples"`
	Years           []YearRow   `json:"years"`
	BusiestDays     []Count     `json:"busiestDays"`
	SourceFolders   []FolderRow `json:"sourceFolders"`
}

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type YearRow struct {
	Year                   int   `json:"year"`
	Photos, Videos         int   `json:"-"`
	PhotoBytes, VideoBytes int64 `json:"-"`
}

type FolderRow struct {
	Folder    string `json:"folder"`
	Items     int    `json:"items"`
	Photos    int    `json:"photos"`
	Videos    int    `json:"videos"`
	FirstYear int    `json:"firstYear"`
	LastYear  int    `json:"lastYear"`
}

// Summarize adds up a plan.
func Summarize(person string, sources []Source, entries []Entry, unmatched []Sidecar) Summary {
	s := Summary{Person: person, CreatedAt: time.Now().UTC()}
	years := map[int]*YearRow{}
	days := map[string]int{}
	folders := map[string]*FolderRow{}
	byAsset := map[string]Source{}
	for _, src := range sources {
		byAsset[src.AssetID.String()] = src
		s.CopiesLeft += src.Copies - 1
		s.CopiesLeftBytes += src.OtherBytes
		if src.LiveMotion.Valid {
			s.LivePhotos++
		}
		if src.StackCover.Valid {
			s.Stacked++
		}
	}
	for _, e := range entries {
		if e.Role == "sidecar" {
			s.Sidecars++
			s.SidecarBytes += e.Size
			continue
		}
		// library/<person>/<photos|videos>/<yyyy>/<yyyy-mm-dd>/<name>
		parts := strings.Split(e.Dst, "/")
		kind, year, dayFolder := parts[2], atoi(parts[3]), strings.Join(parts[:5], "/")
		y := years[year]
		if y == nil {
			y = &YearRow{Year: year}
			years[year] = y
		}
		if kind == "photos" {
			s.Photos++
			s.PhotoBytes += e.Size
			y.Photos++
			y.PhotoBytes += e.Size
		} else {
			s.Videos++
			s.VideoBytes += e.Size
			y.Videos++
			y.VideoBytes += e.Size
		}
		days[dayFolder]++
		if e.Renamed {
			s.Renamed++
			if len(s.RenamedExamples) < 12 {
				s.RenamedExamples = append(s.RenamedExamples, fmt.Sprintf("%s/%s → %s", e.Library, e.Src, strings.TrimPrefix(e.Dst, "library/"+person+"/")))
			}
		}
		src := byAsset[e.AssetID.UUID.String()]
		f := e.Library + "/" + topFolders(src.Rel, 2)
		fr := folders[f]
		if fr == nil {
			fr = &FolderRow{Folder: f, FirstYear: year, LastYear: year}
			folders[f] = fr
		}
		fr.Items++
		if kind == "photos" {
			fr.Photos++
		} else {
			fr.Videos++
		}
		fr.FirstYear, fr.LastYear = min(fr.FirstYear, year), max(fr.LastYear, year)
	}
	s.Items = len(sources)
	for _, y := range years {
		s.Years = append(s.Years, *y)
	}
	sort.Slice(s.Years, func(i, j int) bool { return s.Years[i].Year > s.Years[j].Year })
	for d, n := range days {
		s.BusiestDays = append(s.BusiestDays, Count{strings.TrimPrefix(d, "library/"+person+"/"), n})
	}
	sort.Slice(s.BusiestDays, func(i, j int) bool { return s.BusiestDays[i].Count > s.BusiestDays[j].Count })
	if len(s.BusiestDays) > 12 {
		s.BusiestDays = s.BusiestDays[:12]
	}
	for _, f := range folders {
		s.SourceFolders = append(s.SourceFolders, *f)
	}
	sort.Slice(s.SourceFolders, func(i, j int) bool { return s.SourceFolders[i].Items > s.SourceFolders[j].Items })
	left := map[string]int{}
	for _, sc := range unmatched {
		left[sc.Library+"/"+topFolders(sc.Rel, 1)]++
	}
	for k, v := range left {
		s.SidecarsLeft = append(s.SidecarsLeft, Count{k, v})
	}
	sort.Slice(s.SidecarsLeft, func(i, j int) bool { return s.SidecarsLeft[i].Count > s.SidecarsLeft[j].Count })
	return s
}

// WriteCSV writes every planned copy: role, source, destination, size, renamed.
func WriteCSV(w io.Writer, entries []Entry) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"role", "source", "destination", "bytes", "renamed"})
	for _, e := range entries {
		if err := cw.Write([]string{e.Role, e.Library + "/" + e.Src, e.Dst, strconv.FormatInt(e.Size, 10), strconv.FormatBool(e.Renamed)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// Markdown renders the summary for review.
func (s Summary) Markdown(planID string) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	p("# Move plan (dry run)\n\n")
	p("Plan `%s`, made %s UTC. **Nothing has been copied or moved.** Every file is listed in `plan.csv` next to this summary.\n\n", planID, s.CreatedAt.Format("2006-01-02 15:04"))
	p("Destination: the `photostead` share, `library/%s/photos/<year>/<date>/` and `library/%s/videos/<year>/<date>/`.\n\n", s.Person, s.Person)
	p("## Totals\n\n| | Files | Size |\n|---|---|---|\n")
	p("| Photos | %s | %s |\n", n(s.Photos), gb(s.PhotoBytes))
	p("| Videos | %s | %s |\n", n(s.Videos), gb(s.VideoBytes))
	p("| XMP sidecars travelling with their file | %s | %s |\n", n(s.Sidecars), gb(s.SidecarBytes))
	p("| **To copy** | **%s** | **%s** |\n\n", n(s.Photos+s.Videos+s.Sidecars), gb(s.PhotoBytes+s.VideoBytes+s.SidecarBytes))
	switch {
	case s.LivePhotos == 0 && s.Stacked == 0:
		p("- **%s items.** No Live Photos or RAW+JPEG pairs were found.\n", n(s.Items))
	default:
		p("- **%s items.** %s Live Photos keep their motion clip beside the still in `photos/`; %s items are in RAW+JPEG stacks and share the cover's day.\n", n(s.Items), n(s.LivePhotos), n(s.Stacked))
	}
	p("- **%s duplicate files (%s) are not copied.** Each item is copied once, from its oldest copy; the other copies stay where they are until clean-up.\n", n(s.CopiesLeft), gb(s.CopiesLeftBytes))
	p("- **%s files get a number added to their name** because another item already has that name on that day (names are compared without case, as Windows and macOS do).\n", n(s.Renamed))
	if s.SpareSidecars > 0 {
		p("- **%s sidecars sit beside a second copy of an item that already has one travelling**; they stay where they are.\n", n(s.SpareSidecars))
	}
	if len(s.SidecarsLeft) > 0 {
		total := 0
		for _, c := range s.SidecarsLeft {
			total += c.Count
		}
		p("- **%s sidecars match no photo or video** and stay where they are:", n(total))
		for _, c := range s.SidecarsLeft {
			p(" `%s` (%s)", c.Name, n(c.Count))
		}
		p(".\n")
	}
	p("\n## By year\n\n| Year | Photos | Size | Videos | Size |\n|---|---|---|---|---|\n")
	for _, y := range s.Years {
		p("| %d | %s | %s | %s | %s |\n", y.Year, n(y.Photos), gb(y.PhotoBytes), n(y.Videos), gb(y.VideoBytes))
	}
	p("\n## Fullest day folders\n\nDays with very many items usually mean files without a capture date, dated by when they were copied.\n\n| Folder | Files |\n|---|---|\n")
	for _, d := range s.BusiestDays {
		p("| `%s` | %s |\n", d.Name, n(d.Count))
	}
	if len(s.RenamedExamples) > 0 {
		p("\n## Renamed (examples)\n\n")
		for _, r := range s.RenamedExamples {
			p("- %s\n", r)
		}
	}
	p("\n## Where the old folders go\n\nThe new layout files by date, so the old folder names won't exist in the share. They're kept in Photostead's records (every file's original path), so they can become albums or tags.\n\n| Old folder | Items | Photos | Videos | Spread over |\n|---|---|---|---|---|\n")
	for i, f := range s.SourceFolders {
		if i == 40 {
			break
		}
		span := strconv.Itoa(f.FirstYear)
		if f.LastYear != f.FirstYear {
			span = fmt.Sprintf("%d–%d", f.FirstYear, f.LastYear)
		}
		p("| `%s` | %s | %s | %s | %s |\n", f.Folder, n(f.Items), n(f.Photos), n(f.Videos), span)
	}
	return b.String()
}

func topFolders(rel string, depth int) string {
	dir := path.Dir(rel)
	if dir == "." {
		return "(top level)"
	}
	parts := strings.Split(dir, "/")
	if len(parts) > depth {
		parts = parts[:depth]
	}
	return strings.Join(parts, "/")
}

func atoi(s string) int { v, _ := strconv.Atoi(s); return v }

func n(v int) string {
	s := strconv.Itoa(v)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func gb(b int64) string {
	switch {
	case b >= 1e12:
		return fmt.Sprintf("%.2f TB", float64(b)/1e12)
	case b >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(b)/1e9)
	default:
		return fmt.Sprintf("%.0f MB", float64(b)/1e6)
	}
}

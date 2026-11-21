package metadata

import (
	"fmt"
	"testing"
	"time"
)

func TestParseNameDate(t *testing.T) {
	cases := map[string]string{
		"comfyui-output/lab/CivitAI/Krea2_2026-07-15-000933_66.png":             "2026-07-15 00:09:33",
		"pikaso/pikaso-creations-2025-10-19_11_21/freepik__img1__87685.png":     "2025-10-19 11:21:00",
		"Needs Sorting/txt2img-images/2023-09-12/00114-1943977680.png":          "2023-09-12",
		"camera/IMG_20230101_123456.jpg":                                        "2023-01-01 12:34:56",
		"sloan/Sloan Van De Meer/Sloan Van De Meer-20251223-004435-8047.png":    "2025-12-23 00:44:35",
		"admin/2025/2025-06-27/84854649.mp4":                                    "2025-06-27",
		"incoming/gen-20260308-meta-intent-forensic-replication-1-759add11.png": "2026-03-08",
		"Screenshot 2024-02-03 at 10.11.12.png":                                 "2024-02-03 10:11:12",
		"generated-ai-batches/0fe81419296e36d069a2f923b178e617.jpg":             "",
		"ai/1943977680.png":                                 "",
		"uuid/64409856-022c-445f-befe-ad6c0cc33dbc.mp4":     "",
		"hash/f20231105a.png":                               "",
		"bad/2023-02-30.png":                                "",
		"future/2099-01-01.png":                             "",
		"name beats folder/2020-01-01/photo-2021-05-06.jpg": "2021-05-06",
	}
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for rel, want := range cases {
		nd, ok := ParseNameDate(rel, now)
		got := ""
		if ok {
			got = fmt.Sprintf("%04d-%02d-%02d", nd.Year, nd.Month, nd.Day)
			if nd.HasTime {
				got += fmt.Sprintf(" %02d:%02d:%02d", nd.Hour, nd.Minute, nd.Second)
			}
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", rel, got, want)
		}
	}
}

func TestDateFromCopies(t *testing.T) {
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	mtime := time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC) // 11:00 on 1 May in Los Angeles
	d := DateFromCopies([]CopyTime{{"txt2img-images/2023-09-12/00114.png", mtime}}, la, now)
	if d.Source != "name" || d.LocalDate.Format("2006-01-02") != "2023-09-12" || d.Basis != "camera_clock" {
		t.Errorf("earlier name date: %+v", d)
	}
	if got := d.TakenAt.In(la).Format("15:04"); got != "12:00" {
		t.Errorf("a name without a time is midday, got %s", got)
	}
	// A name date later than the file time is ignored (e.g. a UTC timestamp a day ahead).
	d = DateFromCopies([]CopyTime{{"Krea2_2026-05-02-010000.png", mtime}}, la, now)
	if d.Source != "file-modified" || d.LocalDate.Format("2006-01-02") != "2026-05-01" {
		t.Errorf("later name date should be ignored: %+v", d)
	}
	// The same day as the file time: the file time is more precise.
	d = DateFromCopies([]CopyTime{{"shots/2026-05-01/a.png", mtime}}, la, now)
	if d.Source != "file-modified" {
		t.Errorf("same-day name date should keep the file time: %+v", d)
	}
	// Copies: the earliest evidence across all of them.
	older := time.Date(2025, 1, 2, 20, 0, 0, 0, time.UTC)
	d = DateFromCopies([]CopyTime{{"a/x.png", mtime}, {"b/x.png", older}, {"c/2024-06-01/x.png", mtime}}, la, now)
	if d.Source != "name" || d.LocalDate.Format("2006-01-02") != "2024-06-01" {
		t.Errorf("copies: %+v", d)
	}
	d = DateFromCopies([]CopyTime{{"a/x.png", mtime}, {"b/x.png", older}}, la, now)
	if d.Source != "file-modified" || d.LocalDate.Format("2006-01-02") != "2025-01-02" {
		t.Errorf("earliest file time among copies: %+v", d)
	}
}

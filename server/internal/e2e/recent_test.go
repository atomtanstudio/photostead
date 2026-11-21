package e2e

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
)

// Recently added: what the timeline shows, newest-arrived first whatever the date it was taken, a
// page at a time; archived and trashed items, and a Live Photo's clip, aren't in it.
func TestRecentlyAdded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, base, c := signedIn(t, ctx)

	type page struct {
		Assets []api.RecentAsset
		Next   string
	}
	all := func(query string) (list []api.RecentAsset, pages int) {
		t.Helper()
		cursor := ""
		for {
			var p page
			getJSON(t, c, base+"/api/v1/timeline/recent?limit=2"+query+"&cursor="+url.QueryEscape(cursor), &p)
			list = append(list, p.Assets...)
			pages++
			if p.Next == "" {
				return list, pages
			}
			cursor = p.Next
		}
	}

	// The timeline's items, oldest-taken first, made to have arrived in that order (so the newest
	// arrival is the oldest photo, and Recently added is the timeline backwards).
	var buckets struct{ Buckets []api.Bucket }
	getJSON(t, c, base+"/api/v1/timeline/buckets", &buckets)
	var taken []api.TimelineAsset
	for i := len(buckets.Buckets) - 1; i >= 0; i-- {
		var month struct{ Assets []api.TimelineAsset }
		getJSON(t, c, base+"/api/v1/timeline?month="+buckets.Buckets[i].Month, &month)
		for j := len(month.Assets) - 1; j >= 0; j-- {
			taken = append(taken, month.Assets[j])
		}
	}
	if len(taken) < 4 {
		t.Fatalf("only %d items in the timeline", len(taken))
	}
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i, a := range taken { // the newest-taken arrives first, the oldest last
		at := start.Add(time.Duration(len(taken)-i) * time.Minute)
		if _, err := s.Pool.Exec(ctx, `UPDATE assets SET created_at = $2 WHERE id = $1`, a.ID, at); err != nil {
			t.Fatal(err)
		}
	}

	list, pages := all("")
	if len(list) != len(taken) || pages != (len(taken)+1)/2 {
		t.Fatalf("recently added = %d items in %d pages, want the timeline's %d in pages of 2", len(list), pages, len(taken))
	}
	for i, a := range list {
		if i > 0 && a.AddedAt.After(list[i-1].AddedAt) {
			t.Errorf("item %d arrived after the one before it", i)
		}
	}
	if list[0].ID != taken[0].ID || list[len(list)-1].ID != taken[len(taken)-1].ID {
		t.Errorf("first and last = %s, %s; want the newest arrival (%s) first and the oldest (%s) last",
			list[0].ID, list[len(list)-1].ID, taken[0].ID, taken[len(taken)-1].ID)
	}
	if !list[0].AddedAt.Equal(start.Add(time.Duration(len(taken)) * time.Minute)) {
		t.Errorf("added at = %v", list[0].AddedAt)
	}
	seen := map[uuid.UUID]bool{}
	for _, a := range list {
		if seen[a.ID] {
			t.Errorf("%s came twice across pages", a.ID)
		}
		seen[a.ID] = true
	}

	// Archived and trashed items leave it.
	archived, trashed := list[0].ID, list[1].ID
	if code := doJSON(t, c, "POST", base+"/api/v1/assets/archive", mustJSON(map[string]any{"assetIds": []uuid.UUID{archived}, "archived": true}), nil); code != 200 {
		t.Fatalf("archive = %d", code)
	}
	if code := doJSON(t, c, "POST", base+"/api/v1/assets/trash", mustJSON(map[string]any{"assetIds": []uuid.UUID{trashed}}), nil); code != 200 {
		t.Fatalf("trash = %d", code)
	}
	after, _ := all("")
	for _, a := range after {
		if a.ID == archived || a.ID == trashed {
			t.Errorf("%s is archived or trashed, and still in recently added", a.ID)
		}
	}
	if len(after) != len(list)-2 {
		t.Errorf("after archiving and trashing one each: %d items, want %d", len(after), len(list)-2)
	}

	// Only videos, when asked.
	videos, _ := all("&type=video")
	for _, a := range videos {
		if a.Type != "video" {
			t.Errorf("type=video gave a %s", a.Type)
		}
	}

	if code := status(t, c, "GET", base+"/api/v1/timeline/recent?cursor=nonsense", ""); code != 422 {
		t.Errorf("a made-up cursor = %d, want 422", code)
	}
}

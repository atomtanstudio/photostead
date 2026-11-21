package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Memories: on this day in earlier years (else the week around it), the best first; never
// screenshots, AI-made images (unless wanted), hidden days, the vault, or people kept out; and a
// named person over the years.
func TestMemories(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, owner, url, c := signedIn(t, ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var items []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT a.id FROM assets a WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
		AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) ORDER BY a.id LIMIT 7`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		items = append(items, id)
	}
	rows.Close()
	if len(items) < 7 {
		t.Fatalf("%d items", len(items))
	}
	exec(`UPDATE assets SET source_type = 'unknown' WHERE id = ANY($1)`, items)
	for i, d := range []string{"2023-05-04", "2023-05-04", "2023-05-04", "2021-05-04", "2024-05-06", "2020-01-10", "2020-03-15"} {
		exec(`UPDATE assets SET local_date = $2 WHERE id = $1`, items[i], d)
	}
	type memory struct {
		Key, Kind, Title, Subtitle, From, To string
		Count                                int32
		Assets                               []struct{ ID uuid.UUID }
		Person                               *struct{ ID uuid.UUID }
	}
	get := func(date string) []memory {
		t.Helper()
		var out struct{ Memories []memory }
		getJSON(t, c, url+"/api/v1/memories?date="+date, &out)
		return out.Memories
	}
	days := func(ms []memory) map[string]int32 {
		got := map[string]int32{}
		for _, m := range ms {
			if m.Kind == "on_this_day" {
				got[m.Title] = m.Count
			}
		}
		return got
	}

	ms := get("2026-05-04")
	if len(ms) < 2 || ms[0].Title != "3 years ago" || ms[0].Subtitle != "May 4, 2023" || ms[0].Count != 3 || ms[1].Title != "5 years ago" {
		t.Fatalf("on this day = %+v", ms)
	}

	// A screenshot never; an AI-made image only when wanted.
	exec(`INSERT INTO asset_labels (asset_id, label, basis, score) VALUES ($1, 'screenshot', 'visual', 0.9)`, items[1])
	exec(`UPDATE assets SET source_type = 'ai_generated' WHERE id = $1`, items[2])
	if n := days(get("2026-05-04"))["3 years ago"]; n != 1 {
		t.Errorf("with a screenshot and an AI image, 3 years ago holds %d", n)
	}
	if code := doJSON(t, c, "PATCH", url+"/api/v1/auth/me/settings", `{"memoriesAi":true}`, nil); code != 200 {
		t.Fatalf("settings = %d", code)
	}
	if n := days(get("2026-05-04"))["3 years ago"]; n != 2 {
		t.Errorf("with AI wanted, 3 years ago holds %d", n)
	}
	_ = doJSON(t, c, "PATCH", url+"/api/v1/auth/me/settings", `{"memoriesAi":false}`, nil)

	// A hidden day, and the vault, are left out.
	var hidden struct{ ID uuid.UUID }
	if code := doJSON(t, c, "POST", url+"/api/v1/memories/hidden", `{"from":"2023-05-04"}`, &hidden); code != 201 {
		t.Fatalf("hiding a day = %d", code)
	}
	if _, ok := days(get("2026-05-04"))["3 years ago"]; ok {
		t.Error("a hidden day is still remembered")
	}
	if code := status(t, c, "DELETE", url+"/api/v1/memories/hidden/"+hidden.ID.String(), ""); code != 204 {
		t.Errorf("unhiding = %d", code)
	}
	exec(`UPDATE assets SET visibility = 'vault' WHERE id = $1`, items[3])
	if _, ok := days(get("2026-05-04"))["5 years ago"]; ok {
		t.Error("an item in the vault is remembered")
	}
	exec(`UPDATE assets SET visibility = 'timeline' WHERE id = $1`, items[3])

	// Nothing on the day itself: the week around it.
	ms = get("2026-05-07")
	if len(ms) == 0 || !strings.HasPrefix(ms[0].Subtitle, "The week of") || ms[0].Title != "2 years ago" {
		t.Errorf("the week around = %+v", ms)
	}

	// A named person over the years; kept out, they and their items go.
	var ada uuid.UUID
	if err := s.Pool.QueryRow(ctx, `INSERT INTO people (owner_id, name) VALUES ($1, 'Ada') RETURNING id`, owner.ID).Scan(&ada); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 1, 3, 4, 5, 6} {
		exec(`INSERT INTO person_suggestions (person_id, asset_id, source) VALUES ($1, $2, 'test')`, ada, items[i])
	}
	var person *memory
	for _, m := range get("2026-05-04") {
		if m.Kind == "person" {
			person = &m
		}
	}
	if person == nil || person.Title != "Ada over the years" || person.Subtitle != "2020 – 2024" || len(person.Assets) != 5 {
		t.Fatalf("Ada's memory = %+v", person)
	}
	if code := doJSON(t, c, "PATCH", url+"/api/v1/people/"+ada.String(), `{"inMemories":false}`, nil); code != 200 {
		t.Fatalf("keeping Ada out = %d", code)
	}
	ms = get("2026-05-04")
	for _, m := range ms {
		if m.Kind == "person" {
			t.Error("Ada is still remembered")
		}
	}
	if _, ok := days(ms)["3 years ago"]; ok {
		t.Error("an item with Ada in it is still remembered")
	}
	var set struct{ HiddenPeople []struct{ Name string } }
	getJSON(t, c, url+"/api/v1/memories/settings", &set)
	if len(set.HiddenPeople) != 1 || set.HiddenPeople[0].Name != "Ada" {
		t.Errorf("kept out = %+v", set.HiddenPeople)
	}
}

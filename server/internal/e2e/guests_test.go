package e2e

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
)

// Removing a guest: the admin removes someone an album was shared with by a personal link. Their
// devices are signed out and their link stops; their comments and picks go; the album, its photos
// and what they added stay, and the owner's activity and sync feed carry on without them. Only a
// guest, only by the admin, and not one who owns things.
func TestRemoveGuest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, url, c := signedIn(t, ctx)
	var items []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT a.id FROM assets a WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
		AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) ORDER BY a.id LIMIT 2`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		items = append(items, id)
	}
	rows.Close()
	var album struct{ ID uuid.UUID }
	if code := doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Holiday", "assetIds": items}), &album); code != 201 {
		t.Fatalf("album = %d", code)
	}
	a := url + "/api/v1/albums/" + album.ID.String()

	// Gran: a personal link, open on her phone from the internet; she comments and picks.
	var people struct{ Members []api.AlbumPerson }
	if code := doJSON(t, c, "POST", a+"/people", `{"name":"Gran","role":"contributor"}`, &people); code != 201 || len(people.Members) != 1 {
		t.Fatalf("sharing with Gran = %d %+v", code, people.Members)
	}
	gran := people.Members[0]
	token := strings.TrimPrefix(gran.Link.URL, url+"/p/")
	h, _ := api.Handler(api.Options{Store: s})
	internet := httptest.NewServer(api.PublicOnly(h))
	defer internet.Close()
	browser := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	phone := browser()
	if code := status(t, phone, "POST", internet.URL+"/api/v1/public/personal/"+token, ""); code != 200 {
		t.Fatalf("Gran opening her link = %d", code)
	}
	ia := internet.URL + "/api/v1/albums/" + album.ID.String()
	if code := doJSON(t, phone, "POST", ia+"/comments", `{"body":"What a trip!"}`, nil); code != 201 {
		t.Fatalf("Gran commenting = %d", code)
	}
	if code := doJSON(t, phone, "POST", ia+"/comments", mustJSON(map[string]any{"body": "Print this one", "assetId": items[0]}), nil); code != 201 {
		t.Fatalf("Gran commenting on an item = %d", code)
	}
	if code := status(t, phone, "PUT", ia+"/picks/"+items[0].String(), ""); code != 204 {
		t.Fatalf("Gran picking = %d", code)
	}
	if code := doJSON(t, c, "POST", a+"/comments", `{"body":"Glad you like them"}`, nil); code != 201 {
		t.Fatalf("the owner commenting = %d", code)
	}
	// One of the items as if she'd added it (a contributor's upload goes into the owner's library).
	if _, err := s.Pool.Exec(ctx, `UPDATE album_assets SET added_by = $1 WHERE album_id = $2 AND asset_id = $3`, gran.ID, album.ID, items[1]); err != nil {
		t.Fatal(err)
	}
	before := readSync(t, c, url, "")

	// The admin sees her among the guests; a household member sees none.
	var hh api.Household
	getJSON(t, c, url+"/api/v1/household", &hh)
	if len(hh.Guests) != 1 || hh.Guests[0].ID != gran.ID || hh.Guests[0].Name != "Gran" || hh.Guests[0].Albums != 1 || hh.Guests[0].Devices != 1 ||
		hh.Guests[0].AddedBy == "" || hh.Guests[0].Email != "" {
		t.Errorf("the admin's guests = %+v", hh.Guests)
	}
	var inv struct{ Token string }
	doJSON(t, c, "POST", url+"/api/v1/household/invites", `{"role":"adult"}`, &inv)
	sam := browser()
	if code := status(t, sam, "POST", url+"/api/v1/invites/"+inv.Token, mustJSON(map[string]string{"displayName": "Sam", "email": "sam@example.com", "password": "a long password for sam"})); code != 200 {
		t.Fatalf("Sam joining = %d", code)
	}
	var samsView api.Household
	getJSON(t, sam, url+"/api/v1/household", &samsView)
	if len(samsView.Guests) != 0 {
		t.Errorf("a member sees guests: %+v", samsView.Guests)
	}
	var samMe api.User
	getJSON(t, sam, url+"/api/v1/auth/me", &samMe)

	// Refused: by a member who isn't the admin, by the guest herself, for someone in the household
	// (the admin included), for no one, and for a guest who owns an album (given an API key).
	remove := func(cl *http.Client, base string, id uuid.UUID) int {
		t.Helper()
		return status(t, cl, "DELETE", base+"/api/v1/household/guests/"+id.String(), "")
	}
	if code := remove(sam, url, gran.ID); code != 403 {
		t.Errorf("a member removing a guest = %d", code)
	}
	if code := remove(phone, internet.URL, gran.ID); code != 404 {
		t.Errorf("the guest removing herself = %d", code)
	}
	if code := remove(c, url, samMe.ID); code != 409 {
		t.Errorf("removing a household member = %d", code)
	}
	var me api.User
	getJSON(t, c, url+"/api/v1/auth/me", &me)
	if code := remove(c, url, me.ID); code != 409 {
		t.Errorf("the admin removing themselves = %d", code)
	}
	if code := status(t, sam, "GET", url+"/api/v1/auth/me", ""); code != 200 {
		t.Errorf("after that, Sam's session = %d", code)
	}
	if code := remove(c, url, uuid.New()); code != 404 {
		t.Errorf("removing no one = %d", code)
	}
	var owns struct{ Members []api.AlbumPerson }
	doJSON(t, c, "POST", a+"/people", `{"name":"Uncle"}`, &owns)
	var uncle uuid.UUID
	for _, m := range owns.Members {
		if m.Name == "Uncle" {
			uncle = m.ID
		}
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO albums (owner_id, title) VALUES ($1, 'Uncle''s own')`, uncle); err != nil {
		t.Fatal(err)
	}
	if code := remove(c, url, uncle); code != 409 {
		t.Errorf("removing a guest who owns an album = %d", code)
	}
	if _, err := s.Q.GetUser(ctx, uncle); err != nil {
		t.Errorf("the guest who owns an album is gone: %v", err)
	}

	// Removed.
	if code := remove(c, url, gran.ID); code != 204 {
		t.Fatalf("removing Gran = %d", code)
	}
	if code := status(t, phone, "GET", internet.URL+"/api/v1/auth/me", ""); code != 401 {
		t.Errorf("Gran's phone after = %d", code)
	}
	if code := status(t, browser(), "POST", internet.URL+"/api/v1/public/personal/"+token, ""); code != 404 {
		t.Errorf("Gran's link after = %d", code)
	}
	if code := remove(c, url, gran.ID); code != 404 {
		t.Errorf("removing Gran twice = %d", code)
	}
	var left int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE id = $1) + (SELECT count(*) FROM sessions WHERE user_id = $1)
		+ (SELECT count(*) FROM personal_links WHERE user_id = $1) + (SELECT count(*) FROM album_members WHERE user_id = $1)
		+ (SELECT count(*) FROM album_comments WHERE author_id = $1) + (SELECT count(*) FROM album_picks WHERE user_id = $1)
		+ (SELECT count(*) FROM album_assets WHERE added_by = $1)`, gran.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("rows of Gran's left = %d %v", left, err)
	}

	// Her comments and pick are gone; the owner's comment, the album, its photos and what she added stay.
	var comments struct{ Comments []api.Comment }
	getJSON(t, c, a+"/comments", &comments)
	if len(comments.Comments) != 1 || comments.Comments[0].Body != "Glad you like them" {
		t.Errorf("comments after = %+v", comments.Comments)
	}
	var picks struct{ Picks []api.Pick }
	getJSON(t, c, a+"/picks", &picks)
	if len(picks.Picks) != 0 {
		t.Errorf("picks after = %+v", picks.Picks)
	}
	var activity struct{ Activity []api.Activity }
	getJSON(t, c, a+"/activity", &activity)
	added, said := int32(0), 0
	for _, x := range activity.Activity {
		if x.Kind == "added" {
			added += x.Items
		} else {
			said++
		}
		if x.Who == "Gran" {
			t.Errorf("Gran is still in the activity: %+v", x)
		}
	}
	if added != 2 || said != 1 {
		t.Errorf("activity after: %d added, %d comments: %+v", added, said, activity.Activity)
	}
	var got api.Album
	getJSON(t, c, a, &got)
	if got.Count != 2 || got.Members != 1 { // Uncle's still in it
		t.Errorf("the album after = %+v", got)
	}
	for _, id := range items {
		if code := status(t, c, "GET", url+"/api/v1/assets/"+id.String(), ""); code != 200 {
			t.Errorf("item %s after = %d", id, code)
		}
	}
	getJSON(t, c, url+"/api/v1/household", &hh)
	if len(hh.Guests) != 1 || hh.Guests[0].ID != uncle {
		t.Errorf("guests after = %+v", hh.Guests)
	}

	// The owner's phone: the album again, as it was, and none of its items gone.
	after := readSync(t, c, url, before.cursor)
	synced, ok := after.albums[album.ID]
	ids := func(l []uuid.UUID) string {
		s := make([]string, len(l))
		for i, id := range l {
			s[i] = id.String()
		}
		sort.Strings(s)
		return strings.Join(s, ",")
	}
	if !ok || ids(synced.AssetIDs) != ids(before.albums[album.ID].AssetIDs) || len(after.gone) != 0 {
		t.Errorf("the owner's sync after: album %+v (had %v), gone %v", synced, before.albums[album.ID].AssetIDs, after.gone)
	}

	// Nothing else that refers to an account stops one going, or is left behind: every foreign key
	// to users goes with it (or lets go of it), but a library's and an item's owner, which a guest
	// never is.
	keys, _ := s.Pool.Query(ctx, `SELECT conrelid::regclass::text || '.' || a.attname FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass AND c.confdeltype NOT IN ('c', 'n') ORDER BY 1`)
	var held []string
	for keys.Next() {
		var k string
		_ = keys.Scan(&k)
		held = append(held, k)
	}
	keys.Close()
	if strings.Join(held, " ") != "assets.owner_id libraries.owner_id" {
		t.Errorf("keys to users that neither cascade nor set null: %v", held)
	}
}

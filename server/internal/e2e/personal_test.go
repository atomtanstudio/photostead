package e2e

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
)

// Personal links: an album shared with someone by name gives them a link and no account to make.
// It opens on the first 3 devices (from the internet too), not a 4th; where it's open it carries
// on; someone signed in as someone else is told so; the owner signs its devices out or replaces it;
// and a second album shared with them is there at the same link.
func TestPersonalLinks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, url, c := signedIn(t, ctx)
	var items []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT a.id FROM assets a WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
		AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) ORDER BY a.id LIMIT 3`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		items = append(items, id)
	}
	rows.Close()
	var trip, party struct{ ID uuid.UUID }
	doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Trip", "assetIds": items[:2]}), &trip)
	doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Party", "assetIds": items[2:]}), &party)

	var people struct{ Members, Candidates []api.AlbumPerson }
	if code := doJSON(t, c, "POST", url+"/api/v1/albums/"+trip.ID.String()+"/people", `{"name":"Mom"}`, &people); code != 201 {
		t.Fatalf("sharing with someone new = %d", code)
	}
	if len(people.Members) != 1 || people.Members[0].Link == nil || people.Members[0].Email != "" || people.Members[0].Role != "viewer" {
		t.Fatalf("members = %+v", people.Members)
	}
	mom := people.Members[0]
	if l := mom.Link; !strings.HasPrefix(l.URL, url+"/p/") || l.Devices != 0 || l.Opened || l.MaxDevices != 3 {
		t.Errorf("Mom's link = %+v", l)
	}
	token := strings.TrimPrefix(mom.Link.URL, url+"/p/")

	h, _ := api.Handler(api.Options{Store: s})
	internet := httptest.NewServer(api.PublicOnly(h))
	defer internet.Close()
	browser := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	open := func(b *http.Client, tok string) int {
		t.Helper()
		return status(t, b, "POST", internet.URL+"/api/v1/public/personal/"+tok, "")
	}
	phone, laptop, tablet, fourth := browser(), browser(), browser(), browser()
	for i, b := range []*http.Client{phone, laptop, tablet} {
		if code := open(b, token); code != 200 {
			t.Fatalf("device %d opening the link = %d", i+1, code)
		}
	}
	if code := open(fourth, token); code != 403 {
		t.Errorf("a 4th device opening the link = %d", code)
	}
	if code := open(phone, token); code != 200 {
		t.Errorf("opening it again where it's open = %d", code)
	}
	var me api.User
	getJSON(t, phone, internet.URL+"/api/v1/auth/me", &me)
	if me.Role != "guest" || me.DisplayName != "Mom" || me.Email != "" {
		t.Errorf("Mom = %+v", me)
	}
	var albums struct{ Albums []api.Album }
	getJSON(t, phone, internet.URL+"/api/v1/albums", &albums)
	if len(albums.Albums) != 1 || albums.Albums[0].Title != "Trip" {
		t.Errorf("Mom's albums = %+v", albums.Albums)
	}

	// At home too (not only from the internet), a guest reaches only what guests may.
	iu, _ := neturl.Parse(internet.URL)
	tried := false
	for _, ck := range phone.Jar.Cookies(iu) {
		if ck.Name != "ps_session" {
			continue
		}
		tried = true
		home := func(method, path string) int {
			req, _ := http.NewRequest(method, url+path, nil)
			req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			return resp.StatusCode
		}
		if code := home("GET", "/api/v1/albums"); code != 200 {
			t.Errorf("Mom's albums at home = %d", code)
		}
		if code := home("GET", "/api/v1/status"); code != 404 {
			t.Errorf("the server's status, for Mom at home = %d", code)
		}
		if code := home("POST", "/api/v1/uploads"); code != 401 {
			t.Errorf("uploading into a share, for Mom at home = %d", code)
		}
	}
	if !tried {
		t.Error("no session cookie for Mom")
	}

	// Signed in as someone else, the link says so (and doesn't sign them out).
	if code := status(t, c, "POST", url+"/api/v1/public/personal/"+token, ""); code != 409 {
		t.Errorf("opening it signed in as the owner = %d", code)
	}
	if code := status(t, c, "GET", url+"/api/v1/auth/me", ""); code != 200 {
		t.Errorf("after that, the owner's session = %d", code)
	}

	// A second album, at the same link.
	if code := doJSON(t, c, "PUT", url+"/api/v1/albums/"+party.ID.String()+"/people/"+mom.ID.String(), `{"role":"contributor"}`, &people); code != 200 || people.Members[0].Link == nil || people.Members[0].Link.Devices != 3 {
		t.Fatalf("sharing a second album with Mom = %d %+v", code, people.Members)
	}
	albums.Albums = nil
	getJSON(t, laptop, internet.URL+"/api/v1/albums", &albums)
	if len(albums.Albums) != 2 {
		t.Errorf("after a second album, Mom has %d", len(albums.Albums))
	}

	// Only whoever made the link manages it.
	var inv struct{ Token string }
	doJSON(t, c, "POST", url+"/api/v1/household/invites", `{"role":"adult"}`, &inv)
	sam := browser()
	status(t, sam, "POST", url+"/api/v1/invites/"+inv.Token, mustJSON(map[string]string{"displayName": "Sam", "email": "sam@example.com", "password": "a long password for sam"}))
	if code := status(t, sam, "DELETE", url+"/api/v1/guests/"+mom.ID.String()+"/devices", ""); code != 404 {
		t.Errorf("someone else signing out Mom's devices = %d", code)
	}

	// Signed out: its devices stop, and it opens again on 3.
	var link api.PersonalLink
	if code := doJSON(t, c, "DELETE", url+"/api/v1/guests/"+mom.ID.String()+"/devices", "", &link); code != 200 || link.Devices != 0 {
		t.Errorf("signing out Mom's devices = %d %+v", code, link)
	}
	if code := status(t, phone, "GET", internet.URL+"/api/v1/auth/me", ""); code != 401 {
		t.Errorf("a signed-out device = %d", code)
	}
	if code := open(fourth, token); code != 200 {
		t.Errorf("after signing out, a new device = %d", code)
	}

	// A guest invitation from before personal links opens the same way, with no account to make.
	var gi struct{ Token string }
	doJSON(t, c, "POST", url+"/api/v1/albums/"+party.ID.String()+"/guests", `{"name":"Moo","role":"viewer"}`, &gi)
	if code := status(t, c, "POST", url+"/api/v1/invites/"+gi.Token+"/open", ""); code != 409 {
		t.Errorf("opening a guest invitation signed in as the owner = %d", code)
	}
	var info struct{ Guest bool }
	getJSON(t, browser(), internet.URL+"/api/v1/invites/"+gi.Token, &info)
	if !info.Guest {
		t.Errorf("a guest invitation says %+v", info)
	}
	moo := browser()
	if code := status(t, moo, "POST", internet.URL+"/api/v1/invites/"+gi.Token+"/open", ""); code != 200 {
		t.Fatalf("opening a guest invitation from the internet = %d", code)
	}
	albums.Albums = nil
	getJSON(t, moo, internet.URL+"/api/v1/albums", &albums)
	if len(albums.Albums) != 1 || albums.Albums[0].Title != "Party" {
		t.Errorf("Moo's albums = %+v", albums.Albums)
	}
	if code := status(t, browser(), "POST", internet.URL+"/api/v1/invites/"+gi.Token+"/open", ""); code != 404 {
		t.Errorf("a guest invitation opened twice = %d", code)
	}
	getJSON(t, c, url+"/api/v1/albums/"+party.ID.String()+"/people", &people)
	found := false
	for _, m := range people.Members {
		found = found || m.Name == "Moo" && m.Link != nil && m.Link.Devices == 1
	}
	if !found {
		t.Errorf("Moo isn't in the album's people with a link: %+v", people.Members)
	}

	// A guest invitation not opened yet can be cancelled by the album's owner (only), and stops.
	var withInvites struct {
		Invites []struct{ ID uuid.UUID }
	}
	doJSON(t, c, "POST", url+"/api/v1/albums/"+party.ID.String()+"/guests", `{"name":"Nobody","role":"viewer"}`, &gi)
	getJSON(t, c, url+"/api/v1/albums/"+party.ID.String()+"/people", &withInvites)
	if len(withInvites.Invites) != 1 {
		t.Fatalf("waiting invitations = %+v", withInvites.Invites)
	}
	invite := withInvites.Invites[0].ID.String()
	if code := status(t, sam, "DELETE", url+"/api/v1/albums/"+party.ID.String()+"/invites/"+invite, ""); code != 404 {
		t.Errorf("someone else cancelling the invitation = %d", code)
	}
	if code := status(t, c, "DELETE", url+"/api/v1/albums/"+trip.ID.String()+"/invites/"+invite, ""); code != 404 {
		t.Errorf("cancelling it through another album = %d", code)
	}
	if code := doJSON(t, c, "DELETE", url+"/api/v1/albums/"+party.ID.String()+"/invites/"+invite, "", &withInvites); code != 200 || len(withInvites.Invites) != 0 {
		t.Errorf("cancelling the invitation = %d %+v", code, withInvites.Invites)
	}
	if code := status(t, browser(), "POST", internet.URL+"/api/v1/invites/"+gi.Token+"/open", ""); code != 404 {
		t.Errorf("a cancelled invitation = %d", code)
	}

	// Replaced: the old link stops, and so do its devices.
	if code := doJSON(t, c, "POST", url+"/api/v1/guests/"+mom.ID.String()+"/link", "", &link); code != 200 || link.URL == mom.Link.URL || link.Devices != 0 {
		t.Errorf("a new link = %d %+v", code, link)
	}
	if code := open(browser(), token); code != 404 {
		t.Errorf("the old link = %d", code)
	}
	if code := status(t, fourth, "GET", internet.URL+"/api/v1/albums", ""); code != 401 {
		t.Errorf("a device of the old link = %d", code)
	}
	if code := open(phone, strings.TrimPrefix(link.URL, url+"/p/")); code != 200 {
		t.Errorf("the new link = %d", code)
	}
}

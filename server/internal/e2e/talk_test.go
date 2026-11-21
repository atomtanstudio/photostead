package e2e

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
)

// Talking about a shared album, and picking from it: a guest (from the internet) comments on the
// album and on an item, picks one; the owner sees everyone's picks and the activity; a comment
// about an item not in the album, or from someone it isn't shared with, is refused; only who said
// it (or the owner) takes a comment back.
func TestAlbumTalk(t *testing.T) {
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
	var album struct{ ID uuid.UUID }
	if code := doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Proofs", "assetIds": items[:2]}), &album); code != 201 {
		t.Fatalf("album = %d", code)
	}
	a := url + "/api/v1/albums/" + album.ID.String()
	h, _ := api.Handler(api.Options{Store: s})
	internet := httptest.NewServer(api.PublicOnly(h))
	defer internet.Close()
	var inv struct{ Token string }
	doJSON(t, c, "POST", a+"/guests", `{"name":"Ivy","role":"viewer"}`, &inv)
	jar, _ := cookiejar.New(nil)
	ivy := &http.Client{Jar: jar}
	if code := status(t, ivy, "POST", internet.URL+"/api/v1/invites/"+inv.Token, mustJSON(map[string]string{"displayName": "Ivy", "email": "ivy@example.com", "password": "a long password for ivy"})); code != 200 {
		t.Fatalf("the guest joining = %d", code)
	}
	ia := internet.URL + "/api/v1/albums/" + album.ID.String()

	// The guest, from the internet: about the album, about an item, and not about one that isn't in it.
	var said api.Comment
	if code := doJSON(t, ivy, "POST", ia+"/comments", `{"body":"Lovely set!"}`, &said); code != 201 || !said.Mine || said.Author != "Ivy" {
		t.Fatalf("the guest commenting = %d %+v", code, said)
	}
	if code := doJSON(t, ivy, "POST", ia+"/comments", mustJSON(map[string]any{"body": "This one please", "assetId": items[0]}), nil); code != 201 {
		t.Errorf("about an item = %d", code)
	}
	if code := doJSON(t, ivy, "POST", ia+"/comments", mustJSON(map[string]any{"body": "Not in it", "assetId": items[2]}), nil); code != 404 {
		t.Errorf("about an item not in the album = %d", code)
	}
	for _, it := range []uuid.UUID{items[0], items[2]} {
		status(t, ivy, "PUT", ia+"/picks/"+it.String(), "")
	}

	// The owner: both comments, Ivy's pick (not the item that isn't in the album), the activity.
	var comments struct{ Comments []api.Comment }
	getJSON(t, c, a+"/comments", &comments)
	if len(comments.Comments) != 2 || !comments.Comments[0].Guest || !comments.Comments[0].Mine {
		t.Errorf("the owner's comments = %+v", comments.Comments)
	}
	var mine struct{ Comments []api.Comment }
	getJSON(t, c, a+"/comments?asset="+items[0].String(), &mine)
	if len(mine.Comments) != 1 || mine.Comments[0].Body != "This one please" {
		t.Errorf("about the item = %+v", mine.Comments)
	}
	var picks struct{ Picks []api.Pick }
	getJSON(t, c, a+"/picks", &picks)
	if len(picks.Picks) != 1 || picks.Picks[0].AssetID != items[0] || len(picks.Picks[0].People) != 1 || picks.Picks[0].People[0] != "Ivy" || picks.Picks[0].Mine {
		t.Errorf("the owner's picks = %+v", picks.Picks)
	}
	var activity struct{ Activity []api.Activity }
	getJSON(t, c, a+"/activity", &activity)
	kinds := map[string]int{}
	for _, x := range activity.Activity {
		kinds[x.Kind]++
	}
	if kinds["comment"] != 2 || kinds["added"] != 1 {
		t.Errorf("activity = %+v", activity.Activity)
	}

	// Someone it isn't shared with sees none of it; Ivy can't take back the owner's comment, the
	// owner can take back Ivy's.
	var ownerSaid api.Comment
	doJSON(t, c, "POST", a+"/comments", `{"body":"Thanks!"}`, &ownerSaid)
	if code := status(t, ivy, "DELETE", ia+"/comments/"+ownerSaid.ID.String(), ""); code != 403 {
		t.Errorf("the guest taking back the owner's comment = %d", code)
	}
	if code := status(t, c, "DELETE", a+"/comments/"+said.ID.String(), ""); code != 204 {
		t.Errorf("the owner taking back the guest's = %d", code)
	}
	var other struct{ ID uuid.UUID }
	doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Private", "assetIds": items[2:]}), &other)
	if code := status(t, ivy, "GET", internet.URL+"/api/v1/albums/"+other.ID.String()+"/comments", ""); code != 404 {
		t.Errorf("the guest reading another album's comments = %d", code)
	}
	if code := status(t, ivy, "POST", internet.URL+"/api/v1/albums/"+other.ID.String()+"/comments", `{"body":"hi"}`); code != 404 {
		t.Errorf("the guest commenting on another album = %d", code)
	}
}

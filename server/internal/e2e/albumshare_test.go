package e2e

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/store/db"
)

// Albums shared with people: a household member and an invited guest see the album (not in their
// timeline), a viewer can't add, a contributor can (uploads go to the owner's library), a guest
// has no household, and someone taken off sees nothing more.
func TestAlbumSharing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, owner, url, c := signedIn(t, ctx)
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
	if code := doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Trip", "assetIds": items[:2]}), &album); code != 201 {
		t.Fatalf("album = %d", code)
	}
	browser := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	join := func(token, email string) *http.Client {
		t.Helper()
		b := browser()
		body := mustJSON(map[string]string{"displayName": email[:3], "email": email, "password": "a long password for " + email})
		if code := status(t, b, "POST", url+"/api/v1/invites/"+token, body); code != 200 {
			t.Fatalf("%s joining = %d", email, code)
		}
		return b
	}
	var inv struct{ Token string }
	doJSON(t, c, "POST", url+"/api/v1/household/invites", `{"role":"adult"}`, &inv)
	sam := join(inv.Token, "sam@example.com")

	// Shared with Sam to see.
	var people struct {
		Members, Candidates []api.AlbumPerson
	}
	getJSON(t, c, url+"/api/v1/albums/"+album.ID.String()+"/people", &people)
	if len(people.Candidates) != 1 || people.Candidates[0].Email != "sam@example.com" {
		t.Fatalf("candidates = %+v", people.Candidates)
	}
	samID := people.Candidates[0].ID
	if code := doJSON(t, c, "PUT", url+"/api/v1/albums/"+album.ID.String()+"/people/"+samID.String(), `{"role":"viewer"}`, &people); code != 200 || len(people.Members) != 1 {
		t.Fatalf("sharing with Sam = %d %+v", code, people)
	}
	var albums struct{ Albums []api.Album }
	getJSON(t, sam, url+"/api/v1/albums", &albums)
	if len(albums.Albums) != 1 || albums.Albums[0].Role != "viewer" || albums.Albums[0].Owner == nil || albums.Albums[0].Count != 2 {
		t.Fatalf("Sam's albums = %+v", albums.Albums)
	}
	total := func(cl *http.Client, q string) int64 {
		t.Helper()
		var b struct{ Total int64 }
		getJSON(t, cl, url+"/api/v1/timeline/buckets"+q, &b)
		return b.Total
	}
	if n := total(sam, ""); n != 0 {
		t.Errorf("Sam's timeline has %d items from a shared album", n)
	}
	if n := total(sam, "?collection=album:"+album.ID.String()); n != 2 {
		t.Errorf("the shared album shows Sam %d items", n)
	}
	if code := status(t, sam, "GET", url+"/api/v1/assets/"+items[0].String()+"/thumbnail", ""); code != 200 {
		t.Errorf("a shared album's thumbnail, to Sam = %d", code)
	}
	if code := status(t, sam, "GET", url+"/api/v1/assets/"+items[2].String(), ""); code != 404 {
		t.Errorf("an item not in the album, to Sam = %d", code)
	}
	var changed struct{ Changed int64 }
	if code := doJSON(t, sam, "POST", url+"/api/v1/albums/"+album.ID.String()+"/add", mustJSON(map[string]any{"assetIds": items[:1]}), &changed); code == 200 && changed.Changed != 0 {
		t.Errorf("a viewer added to the album: %+v", changed)
	}

	// As a contributor, Sam's uploads go into the owner's library, in the album.
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photostead", Kind: "managed", RootPath: t.TempDir(), SidecarMode: "shadow"}); err != nil {
		t.Fatal(err)
	}
	upload := func(cl *http.Client) int {
		t.Helper()
		photo, _ := os.ReadFile("../testdata/library/photo-tokyo.jpg")
		var form bytes.Buffer
		mw := multipart.NewWriter(&form)
		fw, _ := mw.CreateFormFile("file", "from-sam.jpg")
		_, _ = fw.Write(photo)
		_ = mw.Close()
		req, _ := http.NewRequest("POST", url+"/api/v1/albums/"+album.ID.String()+"/upload", &form)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := upload(sam); code != 403 {
		t.Errorf("a viewer uploading = %d", code)
	}
	doJSON(t, c, "PUT", url+"/api/v1/albums/"+album.ID.String()+"/people/"+samID.String(), `{"role":"contributor"}`, nil)
	if code := upload(sam); code != 202 {
		t.Fatalf("a contributor uploading = %d", code)
	}
	var who, desc, albumTitle string
	_ = s.Pool.QueryRow(ctx, `SELECT u.email, up.description, up.album FROM uploads up JOIN users u ON u.id = up.owner_id WHERE up.client LIKE 'album:%'`).Scan(&who, &desc, &albumTitle)
	if who != adminEmail || albumTitle != "Trip" || desc != "Added by sam." {
		t.Errorf("the upload went to %s, album %q, with %q", who, albumTitle, desc)
	}

	// A guest: invited to the album, in no household.
	if code := doJSON(t, c, "POST", url+"/api/v1/albums/"+album.ID.String()+"/guests", `{"name":"Gus","role":"viewer"}`, &inv); code != 201 {
		t.Fatalf("inviting a guest = %d", code)
	}
	gus := join(inv.Token, "gus@example.com")
	var me api.User
	getJSON(t, gus, url+"/api/v1/auth/me", &me)
	if me.Role != "guest" {
		t.Errorf("the guest's account is a %s", me.Role)
	}
	albums.Albums = nil
	getJSON(t, gus, url+"/api/v1/albums", &albums)
	if len(albums.Albums) != 1 || albums.Albums[0].Role != "viewer" {
		t.Errorf("the guest's albums = %+v", albums.Albums)
	}
	if code := status(t, gus, "GET", url+"/api/v1/household", ""); code != 404 {
		t.Errorf("a guest's household = %d", code)
	}
	if n := total(gus, "?collection=album:"+album.ID.String()); n != 2 {
		t.Errorf("the album shows the guest %d items", n)
	}

	// From the internet (through Funnel): a guest joins and sees their album; members can't come in.
	h, _ := api.Handler(api.Options{Store: s})
	internet := httptest.NewServer(api.PublicOnly(h))
	defer internet.Close()
	var guestInv struct {
		Token, URL string
		Public     bool
	}
	doJSON(t, c, "POST", url+"/api/v1/albums/"+album.ID.String()+"/guests", `{"name":"Ivy","role":"contributor"}`, &guestInv)
	if guestInv.URL != url+"/#/invite/"+guestInv.Token || guestInv.Public {
		t.Errorf("with no outside address, the invitation is %q (public %v)", guestInv.URL, guestInv.Public)
	}
	ivy := browser()
	if code := status(t, ivy, "POST", internet.URL+"/api/v1/invites/"+guestInv.Token, mustJSON(map[string]string{"displayName": "Ivy", "email": "ivy@example.com", "password": "a long password for ivy"})); code != 200 {
		t.Fatalf("a guest joining from the internet = %d", code)
	}
	albums.Albums = nil
	getJSON(t, ivy, internet.URL+"/api/v1/albums", &albums)
	if len(albums.Albums) != 1 || albums.Albums[0].Role != "contributor" {
		t.Errorf("from the internet, the guest's albums = %+v", albums.Albums)
	}
	var b struct{ Total int64 }
	getJSON(t, ivy, internet.URL+"/api/v1/timeline/buckets?collection=album:"+album.ID.String(), &b)
	if b.Total < 2 {
		t.Errorf("from the internet, the album shows the guest %d items", b.Total)
	}
	for path, want := range map[string]int{"/api/v1/assets/" + items[0].String(): 200, "/api/v1/household": 404, "/api/v1/auth/sessions": 404, "/api/v1/docs": 404} {
		if code := status(t, ivy, "GET", internet.URL+path, ""); code != want {
			t.Errorf("from the internet, the guest's %s = %d", path, code)
		}
	}
	photo, _ := os.ReadFile("../testdata/library/photo-tokyo.jpg")
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	fw, _ := mw.CreateFormFile("file", "from-ivy.jpg")
	_, _ = fw.Write(photo)
	_ = mw.Close()
	if resp, err := ivy.Post(internet.URL+"/api/v1/albums/"+album.ID.String()+"/upload", mw.FormDataContentType(), &form); err != nil || resp.StatusCode != 202 {
		t.Errorf("a guest contributor adding from the internet = %v %v", resp, err)
	}
	// Sam (a member) can't sign in there, nor the owner's session be used there.
	if code := status(t, browser(), "POST", internet.URL+"/api/v1/auth/login", mustJSON(map[string]string{"email": "sam@example.com", "password": "a long password for sam@example.com"})); code != 403 {
		t.Errorf("a member signing in from the internet = %d", code)
	}
	req, _ := http.NewRequest("GET", internet.URL+"/api/v1/auth/me", nil)
	home, _ := http.NewRequest("GET", url, nil)
	for _, k := range c.Jar.Cookies(home.URL) {
		req.AddCookie(k)
	}
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Errorf("the owner's session from the internet = %v %v", resp, err)
	}
	doJSON(t, c, "POST", url+"/api/v1/household/invites", `{"role":"adult"}`, &inv)
	if code := status(t, browser(), "POST", internet.URL+"/api/v1/invites/"+inv.Token, mustJSON(map[string]string{"displayName": "Max", "email": "max@example.com", "password": "a long password for max"})); code != 403 {
		t.Errorf("joining the household from the internet = %d", code)
	}
	var setup struct{ Needed, Internet bool }
	getJSON(t, browser(), internet.URL+"/api/v1/setup", &setup)
	if !setup.Internet || setup.Needed {
		t.Errorf("from the internet, setup says %+v", setup)
	}

	// Taken off: nothing more.
	if code := status(t, c, "DELETE", url+"/api/v1/albums/"+album.ID.String()+"/people/"+samID.String(), ""); code != 200 {
		t.Errorf("taking Sam off = %d", code)
	}
	if code := status(t, sam, "GET", url+"/api/v1/assets/"+items[0].String(), ""); code != 404 {
		t.Errorf("after being taken off, an album item to Sam = %d", code)
	}
	// The owner's own counts are still their library's.
	var counts api.Counts
	getJSON(t, c, url+"/api/v1/library/counts", &counts)
	if int64(counts.All) != total(c, "") {
		t.Errorf("counts say %d, the timeline %d", counts.All, total(c, ""))
	}
}

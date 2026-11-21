package e2e

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/store/db"
)

// Share links: anyone with the link sees the album's items and nothing else; a password, downloads
// (with the location taken out) and uploads are the owner's to allow; a deleted link stops at once;
// and the internet side (PublicOnly) reaches only what links need.
func TestShareLinks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, owner, url, c := signedIn(t, ctx)
	var items []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT a.id FROM assets a WHERE a.type = 'image' AND a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
		AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) ORDER BY a.id LIMIT 3`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		items = append(items, id)
	}
	rows.Close()
	var album struct{ ID uuid.UUID }
	if code := doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Beach day", "assetIds": items[:2]}), &album); code != 201 {
		t.Fatalf("album = %d", code)
	}
	var link api.Link
	if code := doJSON(t, c, "POST", url+"/api/v1/albums/"+album.ID.String()+"/links", "{}", &link); code != 201 || !strings.Contains(link.URL, "/s/") || link.Public {
		t.Fatalf("link = %d %+v", code, link)
	}
	if !strings.HasPrefix(link.QR, "data:image/png;base64,") {
		t.Errorf("no QR code with the link: %.40q", link.QR)
	}
	token := link.URL[strings.LastIndex(link.URL, "/")+1:]
	pub := url + "/api/v1/public/links/" + token
	jar, _ := cookiejar.New(nil)
	visitor := &http.Client{Jar: jar}

	var shared api.SharedAlbum
	getJSON(t, visitor, pub, &shared)
	if shared.Title != "Beach day" || shared.NeedsPassword || shared.AllowDownload {
		t.Errorf("shared album = %+v", shared)
	}
	var list struct{ Items []api.SharedItem }
	getJSON(t, visitor, pub+"/items", &list)
	if len(list.Items) != 2 {
		t.Fatalf("%d shared items", len(list.Items))
	}
	if code := status(t, visitor, "GET", pub+"/assets/"+items[0].String()+"/thumbnail", ""); code != 200 {
		t.Errorf("a shared thumbnail = %d", code)
	}
	if code := status(t, visitor, "GET", pub+"/assets/"+items[2].String()+"/preview", ""); code != 404 {
		t.Errorf("an item not in the album = %d", code)
	}
	if code := status(t, visitor, "GET", pub+"/assets/"+items[0].String()+"/download", ""); code != 403 {
		t.Errorf("a download not allowed = %d", code)
	}

	// A password, and downloads with the location taken out.
	settings := mustJSON(map[string]any{"password": "sand and sea", "allowDownload": true, "allowUpload": true})
	if code := doJSON(t, c, "PATCH", url+"/api/v1/links/"+link.ID.String(), settings, &link); code != 200 || !link.HasPassword || !link.AllowDownload {
		t.Fatalf("editing the link = %d %+v", code, link)
	}
	jar, _ = cookiejar.New(nil)
	visitor = &http.Client{Jar: jar}
	shared = api.SharedAlbum{}
	getJSON(t, visitor, pub, &shared)
	if !shared.NeedsPassword {
		t.Error("a password-protected album shows without it")
	}
	if code := status(t, visitor, "GET", pub+"/items", ""); code != 401 {
		t.Errorf("items without the password = %d", code)
	}
	if code := status(t, visitor, "POST", pub+"/unlock", `{"password":"nope"}`); code != 403 {
		t.Errorf("a wrong password = %d", code)
	}
	if code := status(t, visitor, "POST", pub+"/unlock", `{"password":"sand and sea"}`); code != 204 && code != 200 {
		t.Fatalf("the right password = %d", code)
	}
	getJSON(t, visitor, pub+"/items", &list)

	// Put a photo that records where it was taken behind the first item.
	var root, rel string
	if err := s.Pool.QueryRow(ctx, `SELECT l.root_path, f.rel_path FROM assets a JOIN files f ON f.id = a.primary_file_id JOIN libraries l ON l.id = f.library_id WHERE a.id = $1`, items[0]).Scan(&root, &rel); err != nil {
		t.Fatal(err)
	}
	tokyo, _ := os.ReadFile("../testdata/library/photo-tokyo.jpg")
	if err := os.WriteFile(filepath.Join(root, rel), tokyo, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE asset_metadata SET lat = 35.68, lon = 139.76 WHERE asset_id = $1`, items[0]); err != nil {
		t.Fatal(err)
	}
	resp := do(t, visitor, "GET", pub+"/assets/"+items[0].String()+"/download", "", nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || len(body) < 1000 || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("a download = %d, %d bytes", resp.StatusCode, len(body))
	}
	got := filepath.Join(t.TempDir(), "got.jpg")
	_ = os.WriteFile(got, body, 0o644)
	if out, _ := exec.Command("exiftool", "-q", "-q", "-s", "-s", "-s", "-GPSLatitude", got).Output(); len(bytes.TrimSpace(out)) != 0 {
		t.Errorf("the download still says where it was taken: %s", out)
	}
	if out, _ := exec.Command("exiftool", "-q", "-q", "-s", "-s", "-s", "-Make", got).Output(); len(bytes.TrimSpace(out)) == 0 {
		t.Error("the download lost the rest of its metadata")
	}

	// Uploads land in the owner's photostead share, for the album.
	managed := t.TempDir()
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photostead", Kind: "managed", RootPath: managed, SidecarMode: "shadow"}); err != nil {
		t.Fatal(err)
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	fw, _ := mw.CreateFormFile("file", "from-sam.jpg")
	_, _ = fw.Write(tokyo)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", pub+"/upload?from=Sam", &form)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	up, err := visitor.Do(req)
	if err != nil || up.StatusCode != 202 {
		t.Fatalf("a visitor's upload = %v %v", up, err)
	}
	up.Body.Close()
	var albumTitle, desc string
	_ = s.Pool.QueryRow(ctx, `SELECT album, description FROM uploads WHERE client = 'share link'`).Scan(&albumTitle, &desc)
	if albumTitle != "Beach day" || desc != "Added by Sam through a share link" {
		t.Errorf("the upload was recorded for %q with %q", albumTitle, desc)
	}

	// The internet side reaches share links, and a guest's way in (no one's signed in here).
	h, _ := api.Handler(api.Options{Store: s})
	public := httptest.NewServer(api.PublicOnly(h))
	defer public.Close()
	for path, want := range map[string]int{"/api/v1/public/links/" + token: 200, "/api/v1/auth/me": 401, "/api/v1/setup": 200,
		"/api/v1/timeline/buckets": 401, "/api/v1/household": 404, "/api/v1/docs": 404, "/api/v1/openapi.json": 404, "/api/v1/auth/oidc/start": 404} {
		if code := status(t, &http.Client{}, "GET", public.URL+path, ""); code != want {
			t.Errorf("from the internet, %s = %d", path, code)
		}
	}

	// Deleted, it stops.
	if code := status(t, c, "DELETE", url+"/api/v1/links/"+link.ID.String(), ""); code != 204 {
		t.Errorf("delete = %d", code)
	}
	if code := status(t, visitor, "GET", pub+"/items", ""); code != 404 {
		t.Errorf("a deleted link = %d", code)
	}
}

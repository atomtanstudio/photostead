package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// A demo server (App Review's, PHOTOSTEAD_DISCARD_UPLOADS): uploads are answered as a new item's
// are, then thrown away. Nothing reaches the library or the share, and a phone's items are noted
// as sent, so neither the app nor iOS sends them again.
func TestDiscardedUploads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
	share := t.TempDir()
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photostead", Kind: "managed", RootPath: share, SidecarMode: "off"}); err != nil {
		t.Fatal(err)
	}
	h, _ := api.Handler(api.Options{Store: s, ServerZone: "UTC", DiscardUploads: true})
	srv := httptest.NewServer(h)
	defer srv.Close()
	key := api.NewAPIKey()
	if _, err := s.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: owner.ID, Name: "phone", TokenHash: api.APIKeyHash(key)}); err != nil {
		t.Fatal(err)
	}
	photo, _ := os.ReadFile("../testdata/library/photo-tokyo.jpg")
	video, _ := os.ReadFile("../testdata/library/generated.png")
	do := func(method, url string, body []byte, hdr map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	meta := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return base64.StdEncoding.EncodeToString(b)
	}
	var reply struct{ Status, SHA256 string }

	// From the web (or Photostead Capture): one multipart upload.
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	mp, _ := mw.CreateFormField("meta")
	_, _ = mp.Write([]byte(`{"fileName":"holiday.jpg","keywords":["beach"]}`))
	fw, _ := mw.CreateFormFile("file", "holiday.jpg")
	_, _ = fw.Write(photo)
	_ = mw.Close()
	resp := do("POST", srv.URL+"/api/v1/uploads", form.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	if resp.StatusCode != 202 || reply.Status != "accepted" || reply.SHA256 == "" {
		t.Errorf("the web's upload = %d %+v", resp.StatusCode, reply)
	}

	// From a phone: iOS's (just the file) and the app's (with its hash), each naming its item.
	resumable := srv.URL + "/api/v1/uploads/resumable"
	resp = do("POST", resumable, video, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0001.MOV", "deviceItem": "A1/L0/001|video"})})
	if resp.StatusCode != 200 {
		t.Errorf("iOS's upload = %d", resp.StatusCode)
	}
	resp = do("POST", resumable, photo, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0002.JPG", "deviceItem": "B2/L0/001|photo", "sha256": reply.SHA256})})
	if resp.StatusCode != 200 {
		t.Errorf("the app's upload = %d", resp.StatusCode)
	}
	// A file that isn't the one described is still refused, as anywhere.
	resp = do("POST", resumable, video, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0003.MOV", "deviceItem": "C3/L0/001|video", "sha256": reply.SHA256})})
	if resp.StatusCode != 422 {
		t.Errorf("a file that isn't the one described = %d", resp.StatusCode)
	}

	// The phone's items count as sent: iOS is turned away from sending one again.
	b, _ := json.Marshal(map[string]any{"items": []string{"A1/L0/001|video", "B2/L0/001|photo", "C3/L0/001|video"}})
	resp = do("POST", srv.URL+"/api/v1/uploads/device-items/check", b, map[string]string{"Content-Type": "application/json"})
	var check struct{ Known []string }
	_ = json.NewDecoder(resp.Body).Decode(&check)
	slices.Sort(check.Known)
	if !slices.Equal(check.Known, []string{"A1/L0/001|video", "B2/L0/001|photo"}) {
		t.Errorf("known = %v", check.Known)
	}
	if resp := do("POST", resumable, video, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0001.MOV", "deviceItem": "A1/L0/001|video"})}); resp.StatusCode != 409 {
		t.Errorf("iOS sending it again = %d", resp.StatusCode)
	}

	// And none of it was kept: no items, no record of the uploads, no files in the share.
	for table, want := range map[string]int{"assets": 0, "uploads": 0} {
		var n int
		if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s: %d rows", table, n)
		}
	}
	var files []string
	_ = filepath.WalkDir(share, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 0 {
		t.Errorf("files left in the share: %v", files)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
}

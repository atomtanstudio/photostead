package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// iOS's background uploads: an earlier draft of resumable uploads (Upload-Incomplete, interop
// version 3, a piece with no content type), and a POST that's just the file. Each names the
// phone's item, which the phone can then ask about; what it sent before it named them is noted
// from its files' hashes, for files the person has.
func TestPhoneBackgroundUploads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
	h, _ := api.Handler(api.Options{Store: s, ServerZone: "UTC"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	key := api.NewAPIKey()
	if _, err := s.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: owner.ID, Name: "phone", TokenHash: api.APIKeyHash(key)}); err != nil {
		t.Fatal(err)
	}
	photo, _ := os.ReadFile("../testdata/library/photo-tokyo.jpg")
	other, _ := os.ReadFile("../testdata/library/generated.png")
	meta := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return base64.StdEncoding.EncodeToString(b)
	}
	do := func(method, url string, body []byte, hdr map[string]string) *http.Response {
		t.Helper()
		var r io.Reader
		if body != nil {
			r = bytes.NewReader(body)
		}
		req, _ := http.NewRequest(method, url, r)
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
	check := func(items ...string) (known, sending []string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"items": items})
		resp := do("POST", srv.URL+"/api/v1/uploads/device-items/check", b, map[string]string{"Content-Type": "application/json"})
		var out struct{ Known, Sending []string }
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != 200 {
			t.Fatalf("check = %d", resp.StatusCode)
		}
		slices.Sort(out.Known)
		slices.Sort(out.Sending)
		return out.Known, out.Sending
	}
	known := func(items ...string) []string {
		t.Helper()
		k, _ := check(items...)
		return k
	}
	base := srv.URL + "/api/v1/uploads/resumable"

	if resp := do("OPTIONS", base, nil, nil); resp.StatusCode != 200 || resp.Header.Get("Upload-Limit") == "" {
		t.Errorf("OPTIONS = %d %v", resp.StatusCode, resp.Header)
	}

	// The earlier draft, cut short: the first half with Upload-Incomplete, a 104 for version 3,
	// then the rest from where the server says, with no content type.
	var early textproto.MIMEHeader
	req, _ := http.NewRequest("POST", base, bytes.NewReader(photo[:len(photo)/2]))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Upload-Draft-Interop-Version", "3")
	req.Header.Set("Upload-Incomplete", "?1")
	req.Header.Set("Photostead-Meta", meta(map[string]any{"fileName": "IMG_0002.JPG", "deviceItem": "A1/L0/001|photo"}))
	req = req.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{Got1xxResponse: func(code int, h textproto.MIMEHeader) error {
		if code == 104 {
			early = h
		}
		return nil
	}}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 201 || loc == "" || resp.Header.Get("Upload-Incomplete") != "?1" {
		t.Fatalf("the first half = %d %v", resp.StatusCode, resp.Header)
	}
	if early == nil || early.Get("Location") != loc || early.Get("Upload-Draft-Interop-Version") != "3" {
		t.Errorf("the 104 said %v", early)
	}
	if k, s := check("A1/L0/001|photo", "B2/L0/001|photo"); len(k) != 0 || !slices.Equal(s, []string{"A1/L0/001|photo"}) {
		t.Errorf("halfway: known %v, on its way %v", k, s)
	}
	resp = do("HEAD", srv.URL+loc, nil, map[string]string{"Upload-Draft-Interop-Version": "3"})
	at, _ := strconv.Atoi(resp.Header.Get("Upload-Offset"))
	if resp.StatusCode != 204 || at != len(photo)/2 || resp.Header.Get("Upload-Incomplete") != "?1" {
		t.Fatalf("how far = %d %v", resp.StatusCode, resp.Header)
	}
	resp = do("PATCH", srv.URL+loc, photo[at:], map[string]string{"Upload-Draft-Interop-Version": "3", "Upload-Offset": strconv.Itoa(at), "Upload-Incomplete": "?0"})
	var reply struct{ Status, SHA256 string }
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	sum := sha256.Sum256(photo)
	if resp.StatusCode >= 300 || reply.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("the rest = %d %+v", resp.StatusCode, reply)
	}

	// Just the file, in one POST: the item's video, and the same photo again from another item
	// (noted all the same, so it isn't sent again).
	resp = do("POST", base, other, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0002.MOV", "deviceItem": "A1/L0/001|pairedVideo"})})
	if resp.StatusCode >= 300 {
		t.Fatalf("one POST = %d", resp.StatusCode)
	}
	resp = do("POST", base, photo, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "copy.jpg", "deviceItem": "B2/L0/001|photo"})})
	if resp.StatusCode >= 300 {
		t.Errorf("the same photo again = %d", resp.StatusCode)
	}
	if k, s := check("A1/L0/001|photo", "A1/L0/001|pairedVideo", "B2/L0/001|photo", "C3/L0/001|photo"); !slices.Equal(k, []string{"A1/L0/001|pairedVideo", "A1/L0/001|photo", "B2/L0/001|photo"}) || len(s) != 0 {
		t.Errorf("known = %v, on their way %v", k, s)
	}

	// iOS's copy of an item that's here, or that the app is sending, is turned away before the
	// file is read; the app's own upload of it isn't, and an item nobody has sent goes ahead.
	if resp := do("POST", base, photo, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0002.JPG", "deviceItem": "A1/L0/001|photo"})}); resp.StatusCode != 409 {
		t.Errorf("iOS sending what's here = %d", resp.StatusCode)
	}
	resp = do("POST", base, nil, map[string]string{"Upload-Draft-Interop-Version": "10", "Upload-Complete": "?0", "Upload-Length": strconv.Itoa(len(other)),
		"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0005.MOV", "deviceItem": "F6/L0/001|video", "sha256": hex.EncodeToString(sha256.New().Sum(nil))})})
	if resp.StatusCode != 201 {
		t.Fatalf("the app starting one = %d", resp.StatusCode)
	}
	if resp := do("POST", base, other, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0005.MOV", "deviceItem": "F6/L0/001|video"})}); resp.StatusCode != 409 {
		t.Errorf("iOS sending what the app is sending = %d", resp.StatusCode)
	}
	if resp := do("POST", base, other, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0006.MOV", "deviceItem": "G7/L0/001|video"})}); resp.StatusCode >= 300 {
		t.Errorf("iOS sending something new = %d", resp.StatusCode)
	}

	// An upload that stopped (nothing came in for a while: the phone went out of reach, the server
	// restarted) no longer counts as on its way: iOS may send the app's item, and the app iOS's.
	var partial string
	if err := s.Pool.QueryRow(ctx, `UPDATE upload_sessions SET created_at = now() - interval '30 minutes'
		WHERE meta->>'deviceItem' = 'F6/L0/001|video' RETURNING path`).Scan(&partial); err != nil {
		t.Fatal(err)
	}
	quiet := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(partial, quiet, quiet); err != nil {
		t.Fatal(err)
	}
	if _, s := check("F6/L0/001|video"); len(s) != 0 {
		t.Errorf("a stalled upload is still on its way: %v", s)
	}
	if resp := do("POST", base, other, map[string]string{"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0005.MOV", "deviceItem": "F6/L0/001|video"})}); resp.StatusCode >= 300 {
		t.Errorf("iOS sending what the app stopped sending = %d", resp.StatusCode)
	}
	if resp := do("POST", base, nil, map[string]string{"Upload-Draft-Interop-Version": "10", "Upload-Complete": "?0", "Upload-Length": strconv.Itoa(len(photo)),
		"Photostead-Meta": meta(map[string]any{"fileName": "IMG_0002.JPG", "deviceItem": "A1/L0/001|photo", "sha256": hex.EncodeToString(sum[:])})}); resp.StatusCode != 201 {
		t.Errorf("the app sending what's here (it asked by hash first) = %d", resp.StatusCode)
	}

	// What the phone sent before: noted for files that are here, not for ones that aren't.
	nothing := sha256.Sum256([]byte("not in the library"))
	b, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"item": "D4/L0/001", "sha256": []string{hex.EncodeToString(sum[:])}},
		{"item": "E5/L0/001", "sha256": []string{hex.EncodeToString(nothing[:])}},
	}})
	resp = do("POST", srv.URL+"/api/v1/uploads/device-items", b, map[string]string{"Content-Type": "application/json"})
	var rec struct{ Recorded int }
	_ = json.NewDecoder(resp.Body).Decode(&rec)
	if resp.StatusCode != 200 || rec.Recorded != 1 {
		t.Errorf("noting = %d %+v", resp.StatusCode, rec)
	}
	if got := known("D4/L0/001", "E5/L0/001"); !slices.Equal(got, []string{"D4/L0/001"}) {
		t.Errorf("known after noting = %v", got)
	}

	// Someone else's phone items are theirs.
	other2, err := s.Q.CreateUser(ctx, db.CreateUserParams{Email: "someone@example.com", Username: "someone", DisplayName: "Someone", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	otherKey := api.NewAPIKey()
	if _, err := s.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: other2.ID, Name: "phone", TokenHash: api.APIKeyHash(otherKey)}); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(map[string]any{"items": []string{"A1/L0/001|photo"}})
	req, _ = http.NewRequest("POST", srv.URL+"/api/v1/uploads/device-items/check", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+otherKey)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	var theirs struct{ Known []string }
	_ = json.NewDecoder(resp.Body).Decode(&theirs)
	if resp.StatusCode != 200 || len(theirs.Known) != 0 {
		t.Errorf("someone else's check = %d %v", resp.StatusCode, theirs.Known)
	}
}

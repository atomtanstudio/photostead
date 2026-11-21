package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// failingBody sends n bytes, then breaks, as a dropped connection would.
type failingBody struct {
	data []byte
	n    int
}

func (b *failingBody) Read(p []byte) (int, error) {
	if b.n <= 0 {
		return 0, errors.New("connection dropped")
	}
	k := copy(p, b.data[:min(b.n, len(b.data))])
	b.data, b.n = b.data[k:], b.n-k
	return k, nil
}

// Resumable uploads: a photo sent in pieces, cut short partway and carried on from where the
// server says it got to, filed like any upload; a wrong offset, a retried last piece, giving up,
// a file that isn't the one described, too many bytes, and someone else's upload.
func TestResumableUploads(t *testing.T) {
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
	sum := sha256.Sum256(photo)
	meta := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return base64.StdEncoding.EncodeToString(b)
	}
	do := func(method, url string, body io.Reader, hdr map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, url, body)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Upload-Draft-Interop-Version", "10")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if _, ok := body.(*failingBody); ok {
			req.ContentLength = -1
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if _, ok := body.(*failingBody); ok {
				return nil
			}
			t.Fatal(err)
		}
		return resp
	}
	base := srv.URL + "/api/v1/uploads/resumable"

	resp := do("OPTIONS", base, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Upload-Limit"), "max-size=") {
		t.Errorf("OPTIONS = %d %q", resp.StatusCode, resp.Header.Get("Upload-Limit"))
	}

	// Made (with nothing sent yet): a 104 then a 201, both saying where it is.
	var early textproto.MIMEHeader
	req, _ := http.NewRequest("POST", base, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Upload-Draft-Interop-Version", "10")
	req.Header.Set("Upload-Complete", "?0")
	req.Header.Set("Upload-Length", strconv.Itoa(len(photo)))
	req.Header.Set("Photostead-Meta", meta(map[string]any{"fileName": "IMG_0001.jpg", "takenAt": "2026-10-12T09:30:00Z", "sha256": hex.EncodeToString(sum[:])}))
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
	if resp.StatusCode != 201 || loc == "" || resp.Header.Get("Upload-Offset") != "0" || resp.Header.Get("Upload-Complete") != "?0" {
		t.Fatalf("making it = %d %v", resp.StatusCode, resp.Header)
	}
	if early == nil || early.Get("Location") != loc || early.Get("Upload-Draft-Interop-Version") != "10" {
		t.Errorf("the 104 said %v", early)
	}
	url := srv.URL + loc
	piece := func(at int, data []byte, last bool) *http.Response {
		return do("PATCH", url, bytes.NewReader(data), map[string]string{"Content-Type": "application/partial-upload",
			"Upload-Offset": strconv.Itoa(at), "Upload-Complete": map[bool]string{true: "?1", false: "?0"}[last]})
	}
	if resp := piece(0, photo[:1000], false); resp.StatusCode != 204 || resp.Header.Get("Upload-Offset") != "1000" {
		t.Fatalf("the first piece = %d %v", resp.StatusCode, resp.Header)
	}
	if resp := piece(500, photo[500:1500], false); resp.StatusCode != 409 || resp.Header.Get("Upload-Offset") != "1000" {
		t.Errorf("a piece from the wrong place = %d %v", resp.StatusCode, resp.Header)
	} else {
		var p map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&p)
		if p["type"] != "https://iana.org/assignments/http-problem-types#mismatching-upload-offset" || p["expected-offset"] != float64(1000) {
			t.Errorf("its problem = %v", p)
		}
	}

	// Cut short partway: what arrived stays, and the server says how much.
	do("PATCH", url, &failingBody{data: photo[1000:], n: 3000}, map[string]string{"Content-Type": "application/partial-upload", "Upload-Offset": "1000", "Upload-Complete": "?1"})
	resp = do("HEAD", url, nil, nil)
	at, _ := strconv.Atoi(resp.Header.Get("Upload-Offset"))
	if resp.StatusCode != 204 || at < 1000 || at > 4000 || resp.Header.Get("Upload-Complete") != "?0" || resp.Header.Get("Upload-Length") != strconv.Itoa(len(photo)) ||
		resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(resp.Header.Get("Upload-Limit"), "max-age=") {
		t.Fatalf("after a dropped piece = %d %v", resp.StatusCode, resp.Header)
	}
	resp = piece(at, photo[at:], true)
	var reply struct{ Status, SHA256, Path string }
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	if resp.StatusCode != 200 || resp.Header.Get("Upload-Complete") != "?1" || reply.Status != "accepted" || reply.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("the last piece = %d %+v", resp.StatusCode, reply)
	}
	if reply.Path != "photos/2026/2026-10-12/IMG_0001.jpg" {
		t.Errorf("filed at %q", reply.Path)
	}
	if got, _ := os.ReadFile(filepath.Join(share, filepath.FromSlash(reply.Path))); !bytes.Equal(got, photo) {
		t.Errorf("the filed file isn't the photo (%d bytes of %d)", len(got), len(photo))
	}
	if left, _ := filepath.Glob(filepath.Join(share, ".uploads", "resumable-*")); len(left) != 0 {
		t.Errorf("partial files left: %v", left)
	}
	// The reply was lost: the last piece again gets it again.
	resp = piece(len(photo), nil, true)
	var again struct{ Path string }
	_ = json.NewDecoder(resp.Body).Decode(&again)
	if resp.StatusCode != 200 || again.Path != reply.Path {
		t.Errorf("the last piece again = %d %+v", resp.StatusCode, again)
	}

	// All at once, with the file name in Content-Disposition.
	resp = do("POST", base, bytes.NewReader(other), map[string]string{"Upload-Complete": "?1", "Content-Disposition": `inline; filename="made.png"`})
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	if resp.StatusCode != 200 || !strings.HasSuffix(reply.Path, "/made.png") {
		t.Errorf("all at once = %d %+v", resp.StatusCode, reply)
	}

	// Not the file described: refused, nothing kept.
	resp = do("POST", base, bytes.NewReader(other), map[string]string{"Upload-Complete": "?1",
		"Photostead-Meta": meta(map[string]any{"fileName": "x.png", "sha256": strings.Repeat("0", 64)})})
	if resp.StatusCode != 422 || resp.Header.Get("Upload-Complete") != "?1" {
		t.Errorf("the wrong file = %d", resp.StatusCode)
	}

	// More than its length: refused, and the upload is over.
	resp = do("POST", base, nil, map[string]string{"Upload-Complete": "?0", "Upload-Length": "10", "Photostead-Meta": meta(map[string]any{"fileName": "y.jpg"})})
	short := srv.URL + resp.Header.Get("Location")
	resp = do("PATCH", short, bytes.NewReader(photo[:20]), map[string]string{"Content-Type": "application/partial-upload", "Upload-Offset": "0", "Upload-Complete": "?1"})
	if resp.StatusCode != 413 {
		t.Errorf("too many bytes = %d", resp.StatusCode)
	}
	if resp := do("HEAD", short, nil, nil); resp.StatusCode != 404 {
		t.Errorf("after too many bytes, the upload = %d", resp.StatusCode)
	}

	// Given up: gone. And someone else can't see an upload.
	resp = do("POST", base, bytes.NewReader(photo[:100]), map[string]string{"Upload-Complete": "?0", "Photostead-Meta": meta(map[string]any{"fileName": "z.jpg"})})
	gone := srv.URL + resp.Header.Get("Location")
	req, _ = http.NewRequest("HEAD", gone, nil)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Errorf("no key = %d", resp.StatusCode)
	}
	if resp := do("DELETE", gone, nil, nil); resp.StatusCode != 204 {
		t.Errorf("giving up = %d", resp.StatusCode)
	}
	if resp := do("HEAD", gone, nil, nil); resp.StatusCode != 404 {
		t.Errorf("after giving up = %d", resp.StatusCode)
	}
	var n int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM upload_sessions WHERE completed_at IS NULL`).Scan(&n)
	if n != 0 {
		t.Errorf("%d uploads still open", n)
	}
}

// A phone away from home backs up through Funnel with its own token: the upload is made (its 104
// coming through too), sent in two pieces with the offset asked for in between, and filed.
func TestResumableUploadThroughFunnel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photostead", Kind: "managed", RootPath: t.TempDir(), SidecarMode: "off"}); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := auth.NewToken()
	token := "psd_" + raw
	if _, err := s.Q.CreateDeviceSession(ctx, db.CreateDeviceSessionParams{TokenHash: auth.TokenHash(token), UserID: owner.ID, ExpiresAt: time.Now().Add(time.Hour),
		Device: pgtype.Text{String: "iPhone", Valid: true}, Platform: pgtype.Text{String: "ios", Valid: true}}); err != nil {
		t.Fatal(err)
	}
	h, _ := api.Handler(api.Options{Store: s, ServerZone: "UTC"})
	internet := httptest.NewServer(api.PairedAway(h, api.PublicOnly(h), s.Q))
	defer internet.Close()

	photo, _ := os.ReadFile("../testdata/library/photo-tokyo.jpg")
	sum := sha256.Sum256(photo)
	m, _ := json.Marshal(map[string]any{"fileName": "IMG_0002.jpg", "takenAt": "2026-11-13T02:28:00Z", "sha256": hex.EncodeToString(sum[:])})
	send := func(method, url string, body []byte, hdr map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Upload-Draft-Interop-Version", "10")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		var early bool
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
			early = early || code == 104
			return nil
		}}))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		if method == "POST" && !early {
			t.Errorf("no 104 came through Funnel")
		}
		return resp
	}
	resp := send("POST", internet.URL+"/api/v1/uploads/resumable", nil, map[string]string{"Upload-Complete": "?0",
		"Upload-Length": strconv.Itoa(len(photo)), "Photostead-Meta": base64.StdEncoding.EncodeToString(m)})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 201 || loc == "" {
		t.Fatalf("making it through Funnel = %d %v", resp.StatusCode, resp.Header)
	}
	piece := map[string]string{"Content-Type": "application/partial-upload", "Upload-Offset": "0", "Upload-Complete": "?0"}
	if resp := send("PATCH", internet.URL+loc, photo[:1000], piece); resp.StatusCode != 204 {
		t.Fatalf("the first piece through Funnel = %d", resp.StatusCode)
	}
	if resp := send("HEAD", internet.URL+loc, nil, nil); resp.StatusCode != 204 || resp.Header.Get("Upload-Offset") != "1000" {
		t.Fatalf("how far it got, through Funnel = %d %v", resp.StatusCode, resp.Header)
	}
	piece["Upload-Offset"], piece["Upload-Complete"] = "1000", "?1"
	if resp := send("PATCH", internet.URL+loc, photo[1000:], piece); resp.StatusCode != 200 || resp.Header.Get("Upload-Complete") != "?1" {
		t.Fatalf("the last piece through Funnel = %d %v", resp.StatusCode, resp.Header)
	}
}

// Through Cloudflare (a request with CF-Ray), the upload's limits ask for pieces of at most 64 MiB,
// which Cloudflare lets through; anywhere else, a file may go in one request. A file sent in three
// pieces arrives whole.
func TestUploadPiecesThroughCloudflare(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.Q.GetUserByEmail(ctx, adminEmail)
	if _, err := s.Q.CreateLibrary(ctx, db.CreateLibraryParams{OwnerID: owner.ID, Name: "photostead", Kind: "managed", RootPath: t.TempDir(), SidecarMode: "off"}); err != nil {
		t.Fatal(err)
	}
	h, _ := api.Handler(api.Options{Store: s, ServerZone: "UTC"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	key := api.NewAPIKey()
	if _, err := s.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{UserID: owner.ID, Name: "phone", TokenHash: api.APIKeyHash(key)}); err != nil {
		t.Fatal(err)
	}
	send := func(method, url string, body []byte, hdr map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Upload-Draft-Interop-Version", "10")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	cf := map[string]string{"CF-Ray": "8e1f2a3b4c5d6e7f-SJC"}
	const piece = "max-append-size=67108864"
	base := srv.URL + "/api/v1/uploads/resumable"
	if l := send("OPTIONS", base, nil, cf).Header.Get("Upload-Limit"); !strings.Contains(l, piece) {
		t.Errorf("OPTIONS through Cloudflare: Upload-Limit %q", l)
	}
	if l := send("OPTIONS", base, nil, nil).Header.Get("Upload-Limit"); strings.Contains(l, "max-append-size") {
		t.Errorf("OPTIONS at home: Upload-Limit %q", l)
	}
	// Behind a reverse proxy that caps requests, PHOTOSTEAD_UPLOAD_PIECE asks for pieces everywhere.
	capped, _ := api.Handler(api.Options{Store: s, ServerZone: "UTC", UploadPiece: 8 << 20})
	proxied := httptest.NewServer(capped)
	defer proxied.Close()
	if l := send("OPTIONS", proxied.URL+"/api/v1/uploads/resumable", nil, nil).Header.Get("Upload-Limit"); !strings.Contains(l, "max-append-size=8388608") {
		t.Errorf("OPTIONS with PHOTOSTEAD_UPLOAD_PIECE=8: Upload-Limit %q", l)
	}

	photo, _ := os.ReadFile("../testdata/library/photo-tokyo.jpg")
	sum := sha256.Sum256(photo)
	m, _ := json.Marshal(map[string]any{"fileName": "IMG_0003.jpg", "takenAt": "2026-11-13T03:00:00Z", "sha256": hex.EncodeToString(sum[:])})
	resp := send("POST", base, nil, map[string]string{"CF-Ray": cf["CF-Ray"], "Upload-Complete": "?0", "Upload-Length": strconv.Itoa(len(photo)),
		"Photostead-Meta": base64.StdEncoding.EncodeToString(m)})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 201 || !strings.Contains(resp.Header.Get("Upload-Limit"), piece) {
		t.Fatalf("made through Cloudflare = %d, Upload-Limit %q", resp.StatusCode, resp.Header.Get("Upload-Limit"))
	}
	third := len(photo) / 3
	for i, cut := range [][2]int{{0, third}, {third, 2 * third}, {2 * third, len(photo)}} {
		last := i == 2
		resp := send("PATCH", srv.URL+loc, photo[cut[0]:cut[1]], map[string]string{"CF-Ray": cf["CF-Ray"], "Content-Type": "application/partial-upload",
			"Upload-Offset": strconv.Itoa(cut[0]), "Upload-Complete": map[bool]string{true: "?1", false: "?0"}[last]})
		if want := map[bool]int{true: 200, false: 204}[last]; resp.StatusCode != want {
			t.Fatalf("piece %d = %d (want %d)", i+1, resp.StatusCode, want)
		}
		if !last {
			head := send("HEAD", srv.URL+loc, nil, cf)
			if head.Header.Get("Upload-Offset") != strconv.Itoa(cut[1]) || !strings.Contains(head.Header.Get("Upload-Limit"), piece) {
				t.Errorf("after piece %d: offset %s, Upload-Limit %q", i+1, head.Header.Get("Upload-Offset"), head.Header.Get("Upload-Limit"))
			}
		}
	}
}

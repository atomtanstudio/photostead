package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// Pairing codes that last: the admin makes one for an account, for some days and devices; each
// device that pairs with it counts, and it stops when its devices or days run out, or it's revoked
// (the devices it paired stay signed in). Only its hash is kept, with a short note to tell it apart.
// Members can't make, list or revoke them; a guest's account can't have one; from the internet they
// pair nothing, as 10-minute codes don't; and behind a proxy on the same machine, one person's wrong
// codes don't stop the next person's right one.
func TestLastingPairingCodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	const public = "https://demo.example.com"
	proxies, err := api.ParseProxies("")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := api.Handler(api.Options{Store: s, PublicURL: public, Proxies: proxies})
	srv := httptest.NewServer(h)
	defer srv.Close()
	base := srv.URL
	browser := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	c := browser()
	if code := status(t, c, "POST", base+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`); code != 200 {
		t.Fatalf("login = %d", code)
	}
	admin, err := s.Q.GetUserByEmail(ctx, adminEmail)
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.Q.CreateUser(ctx, db.CreateUserParams{Email: "grandma@example.com", Username: "grandma", DisplayName: "Grandma", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetPassword(ctx, s.Q, member, "a long member password"); err != nil {
		t.Fatal(err)
	}
	m := browser()
	if code := status(t, m, "POST", base+"/api/v1/auth/login", `{"email":"grandma@example.com","password":"a long member password"}`); code != 200 {
		t.Fatalf("member login = %d", code)
	}
	guest, err := s.Q.CreateUser(ctx, db.CreateUserParams{Email: "guest@example.com", Username: "guest", DisplayName: "Guest", Role: "guest"})
	if err != nil {
		t.Fatal(err)
	}
	var info api.ServerInfo
	getJSON(t, c, base+"/api/v1/server", &info)

	type made struct {
		Code, Link, QR string
		ExpiresAt      time.Time
		Lasting        api.LastingCode
	}
	makeCode := func(body string) made {
		t.Helper()
		var out made
		if code := doJSON(t, c, "POST", base+"/api/v1/devices/pairing/lasting", body, &out); code != 200 {
			t.Fatalf("making a lasting code (%s) = %d", body, code)
		}
		return out
	}
	type paired struct {
		Token string
		User  api.User
	}
	phone := &http.Client{}
	pair := func(code, name string) (int, paired) {
		t.Helper()
		var out paired
		n := doJSON(t, phone, "POST", base+"/api/v1/devices/pair", mustJSON(map[string]string{"code": code, "name": name, "platform": "ios"}), &out)
		return n, out
	}
	list := func() map[uuid.UUID]api.LastingCode {
		t.Helper()
		var out struct{ Codes []api.LastingCode }
		getJSON(t, c, base+"/api/v1/devices/pairing/lasting", &out)
		byID := map[uuid.UUID]api.LastingCode{}
		for _, x := range out.Codes {
			byID[x.ID] = x
		}
		return byID
	}

	// For the admin's own account by default: the code, its link (with the public address) and QR
	// code, shown once, and its note on one line. Only the code's hash is kept.
	two := makeCode(`{"days":3,"devices":2,"note":"  App\nReview "}`)
	if len(two.Code) != 9 || two.Code[4] != '-' || !strings.HasPrefix(two.QR, "data:image/png;base64,") || two.Lasting.UserID != admin.ID ||
		two.Lasting.Name != admin.DisplayName || two.Lasting.MaxDevices != 2 || two.Lasting.Devices != 0 || !two.ExpiresAt.Equal(two.Lasting.ExpiresAt) ||
		two.Lasting.Note != "App Review" {
		t.Fatalf("made = %+v", two)
	}
	if left := time.Until(two.Lasting.ExpiresAt); left < 71*time.Hour || left > 73*time.Hour {
		t.Errorf("a 3-day code ends in %s", left)
	}
	link, _ := url.Parse(two.Link)
	if link.Scheme != "photostead" || link.Query().Get("code") != two.Code || link.Query().Get("server") != info.ID.String() || link.Query().Get("url") != public {
		t.Errorf("link = %s", two.Link)
	}
	var plain int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM pairing_codes p WHERE p::text LIKE '%' || $1 || '%' OR p::text LIKE '%' || $2 || '%'`,
		two.Code, strings.ReplaceAll(two.Code, "-", "")).Scan(&plain); err != nil || plain != 0 {
		t.Errorf("the code itself is in the database: %d rows (%v)", plain, err)
	}

	// Two devices pair with it (typed as someone might), into the admin's account; a third is turned
	// away, and the list shows it used up.
	for _, typed := range []string{two.Code, strings.ToLower(strings.ReplaceAll(two.Code, "-", " "))} {
		if code, p := pair(typed, "Reviewer’s iPhone"); code != 200 || !strings.HasPrefix(p.Token, "psd_") || p.User.Email != adminEmail {
			t.Fatalf("pairing with a lasting code (%q) = %d %+v", typed, code, p)
		}
	}
	if code, _ := pair(two.Code, "A third"); code != 403 {
		t.Errorf("a device over the code's limit = %d", code)
	}
	if got := list()[two.Lasting.ID]; got.Devices != 2 || got.MaxDevices != 2 || got.Email != adminEmail || got.Note != "App Review" {
		t.Errorf("listed = %+v", got)
	}

	// For a member's account (5 devices and 14 days unless said, and no note): the device is theirs,
	// in their list of where they're signed in.
	theirs := makeCode(mustJSON(map[string]any{"userId": member.ID}))
	if theirs.Lasting.UserID != member.ID || theirs.Lasting.Name != "Grandma" || theirs.Lasting.MaxDevices != 5 || theirs.Lasting.Note != "" {
		t.Errorf("made for a member = %+v", theirs.Lasting)
	}
	if got := list()[theirs.Lasting.ID]; got.Note != "" {
		t.Errorf("listed with a note = %+v", got)
	}
	if left := time.Until(theirs.Lasting.ExpiresAt); left < 13*24*time.Hour || left > 15*24*time.Hour {
		t.Errorf("a code by default ends in %s", left)
	}
	code, grandma := pair(theirs.Code, "Grandma’s iPhone")
	if code != 200 || grandma.User.ID != member.ID {
		t.Fatalf("pairing into a member's account = %d %+v", code, grandma)
	}
	var signed struct{ Sessions []api.SignedIn }
	getJSON(t, m, base+"/api/v1/auth/sessions", &signed)
	if len(signed.Sessions) != 2 || (signed.Sessions[0].Device != "Grandma’s iPhone" && signed.Sessions[1].Device != "Grandma’s iPhone") {
		t.Errorf("the member's sessions = %+v", signed.Sessions)
	}

	// Revoked: no more devices pair with it, and the one it paired stays signed in.
	if code := status(t, c, "DELETE", base+"/api/v1/devices/pairing/lasting/"+theirs.Lasting.ID.String(), ""); code != 204 {
		t.Errorf("revoking = %d", code)
	}
	if code, _ := pair(theirs.Code, "After revoking"); code != 403 {
		t.Errorf("a revoked code = %d", code)
	}
	if resp := do(t, phone, "GET", base+"/api/v1/auth/me", "", map[string]string{"Authorization": "Bearer " + grandma.Token}); resp.StatusCode != 200 {
		t.Errorf("a device paired before the code was revoked = %d", resp.StatusCode)
	}
	if code := status(t, c, "DELETE", base+"/api/v1/devices/pairing/lasting/"+theirs.Lasting.ID.String(), ""); code != 404 {
		t.Errorf("revoking it again = %d", code)
	}
	if _, ok := list()[theirs.Lasting.ID]; ok {
		t.Error("a revoked code is still listed")
	}

	// Run out of days: turned away, and no longer listed.
	old := makeCode(`{"days":1}`)
	if _, err := s.Pool.Exec(ctx, "UPDATE pairing_codes SET expires_at = now() - interval '1 minute' WHERE id = $1", old.Lasting.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := pair(old.Code, "Too late"); code != 403 {
		t.Errorf("a code past its days = %d", code)
	}
	if _, ok := list()[old.Lasting.ID]; ok {
		t.Error("a code past its days is still listed")
	}

	// Only the admin makes, lists and revokes them; never for a guest's account; within the limits.
	if code := status(t, m, "POST", base+"/api/v1/devices/pairing/lasting", `{}`); code != 403 {
		t.Errorf("a member making one = %d", code)
	}
	if code := status(t, m, "GET", base+"/api/v1/devices/pairing/lasting", ""); code != 403 {
		t.Errorf("a member listing them = %d", code)
	}
	if code := status(t, m, "DELETE", base+"/api/v1/devices/pairing/lasting/"+two.Lasting.ID.String(), ""); code != 403 {
		t.Errorf("a member revoking one = %d", code)
	}
	for body, want := range map[string]int{mustJSON(map[string]any{"userId": guest.ID}): 422, mustJSON(map[string]any{"userId": uuid.New()}): 404,
		`{"days":91}`: 422, `{"devices":51}`: 422, `{"devices":0}`: 422, mustJSON(map[string]any{"note": strings.Repeat("é", 61)}): 422} {
		if code := status(t, c, "POST", base+"/api/v1/devices/pairing/lasting", body); code != want {
			t.Errorf("making one with %s = %d (want %d)", body, code, want)
		}
	}
	if long := makeCode(mustJSON(map[string]any{"note": strings.Repeat("é", 60)})); long.Lasting.Note != strings.Repeat("é", 60) {
		t.Errorf("a note of 60 characters = %q", long.Lasting.Note)
	}

	// From the internet (Funnel), a lasting code pairs nothing, and a paired phone can't make one.
	internet := httptest.NewServer(api.PairedAway(h, api.PublicOnly(h), s.Q))
	defer internet.Close()
	away := makeCode(`{}`)
	if resp := do(t, phone, "POST", internet.URL+"/api/v1/devices/pair", mustJSON(map[string]string{"code": away.Code, "name": "From away"}), nil); resp.StatusCode != 404 {
		t.Errorf("pairing from the internet = %d", resp.StatusCode)
	}
	if resp := do(t, phone, "POST", internet.URL+"/api/v1/devices/pair", mustJSON(map[string]string{"code": away.Code, "name": "From away"}),
		map[string]string{"Authorization": "Bearer " + grandma.Token}); resp.StatusCode != 403 {
		t.Errorf("pairing from the internet with a paired phone's token = %d", resp.StatusCode)
	}
	if got := list()[away.Lasting.ID]; got.Devices != 0 {
		t.Errorf("tries from the internet counted a device: %+v", got)
	}
	n, mine := pair(away.Code, "Admin’s iPhone")
	if n != 200 {
		t.Fatalf("pairing at home with the code tried from away = %d", n)
	}
	for method, path := range map[string]string{"POST": "/api/v1/devices/pairing/lasting", "DELETE": "/api/v1/devices/pairing/lasting/" + away.Lasting.ID.String()} {
		if resp := do(t, phone, method, internet.URL+path, `{}`, map[string]string{"Authorization": "Bearer " + mine.Token}); resp.StatusCode != 403 {
			t.Errorf("the admin's paired phone from away, %s %s = %d (want 403)", method, path, resp.StatusCode)
		}
	}

	// Made on the command line (as photostead pairing add does): it pairs too, and lists its note.
	cli, lc, err := api.MakeLastingCode(ctx, s.Q, member.ID, 1, 1, "Grandma’s iPad")
	if err != nil || lc.MaxDevices != 1 || lc.UserID != member.ID || lc.Note != "Grandma’s iPad" {
		t.Fatalf("made on the command line = %+v %v", lc, err)
	}
	if got := list()[lc.ID]; got.Note != "Grandma’s iPad" {
		t.Errorf("listed from the command line = %+v", got)
	}
	if _, _, err := api.MakeLastingCode(ctx, s.Q, member.ID, 1, 1, strings.Repeat("x", 61)); err == nil {
		t.Error("a note of 61 characters on the command line")
	}
	cliLink, err := api.PairingLink(ctx, s.Q, cli, public)
	if u, _ := url.Parse(cliLink); err != nil || u.Query().Get("code") != cli || u.Query().Get("server") != info.ID.String() || u.Query().Get("url") != public {
		t.Errorf("its link = %s %v", cliLink, err)
	}
	if code, p := pair(cli, "Grandma’s iPad"); code != 200 || p.User.ID != member.ID {
		t.Errorf("pairing with a code from the command line = %d %+v", code, p)
	}
	if _, _, err := api.MakeLastingCode(ctx, s.Q, guest.ID, 1, 1, ""); !errors.Is(err, api.ErrGuestPairing) {
		t.Errorf("one for a guest on the command line: %v", err)
	}

	// A 10-minute code works beside them, as before.
	var shown struct{ Code string }
	if code := doJSON(t, c, "POST", base+"/api/v1/devices/pairing", "", &shown); code != 200 {
		t.Fatalf("a 10-minute code = %d", code)
	}
	if code, p := pair(shown.Code, "Admin’s iPad"); code != 200 || p.User.ID != admin.ID {
		t.Errorf("pairing with a 10-minute code = %d %+v", code, p)
	}

	// Behind a proxy on the same machine (cloudflared, Caddy), wrong codes count against whom the
	// proxy says sent them, not what they say themselves: ten stop that person, even with the right
	// code after, and App Review still pairs.
	review := makeCode(`{"note":"App Review"}`)
	proxied := func(from, code string) int {
		t.Helper()
		return do(t, phone, "POST", base+"/api/v1/devices/pair", mustJSON(map[string]string{"code": code, "name": "Through the proxy"}),
			map[string]string{"X-Forwarded-For": "6.6.6.6, " + from}).StatusCode
	}
	for i := 1; i <= 10; i++ {
		if code := proxied("203.0.113.7", "WRONG-CODE"); code != 403 {
			t.Fatalf("wrong code %d through the proxy = %d", i, code)
		}
	}
	if code := proxied("203.0.113.7", review.Code); code != 429 {
		t.Errorf("after ten wrong codes through the proxy = %d (want 429)", code)
	}
	if code := proxied("198.51.100.4", review.Code); code != 200 {
		t.Errorf("someone else through the same proxy = %d", code)
	}
}

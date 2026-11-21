package e2e

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// Devices: a pairing code (with the server's id and addresses in its link) pairs a device once; the
// device's token works like a session and shows where the owner is signed in, until they end it.
// The home address is learned from a visit; the Tailscale node is the admin's to turn on.
func TestDevices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	h, _ := api.Handler(api.Options{Store: s})
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

	// A visit at a home-network address is remembered.
	req, _ := http.NewRequest("GET", base+"/api/v1/auth/me", nil)
	req.Host = "192.168.1.10:4800"
	u, _ := url.Parse(base)
	for _, ck := range c.Jar.Cookies(u) { // as the browser there would send it
		req.AddCookie(ck)
	}
	if resp, err := (&http.Client{}).Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("visit at home = %v %v", resp, err)
	}
	var addr api.Addresses
	getJSON(t, c, base+"/api/v1/server/addresses", &addr)
	if addr.Home != "http://192.168.1.10:4800" {
		t.Errorf("addresses = %+v", addr)
	}

	var info api.ServerInfo
	getJSON(t, browser(), base+"/api/v1/server", &info)
	var pairing struct {
		Code, Link, QR string
	}
	if code := doJSON(t, c, "POST", base+"/api/v1/devices/pairing", "", &pairing); code != 200 || len(pairing.Code) != 9 || !strings.HasPrefix(pairing.QR, "data:image/png;base64,") {
		t.Fatalf("pairing = %d %+v", code, pairing)
	}
	link, _ := url.Parse(pairing.Link)
	if link.Scheme != "photostead" || link.Query().Get("code") != pairing.Code || link.Query().Get("server") != info.ID.String() ||
		link.Query().Get("url") != "http://192.168.1.10:4800" {
		t.Errorf("link = %s", pairing.Link)
	}

	phone := &http.Client{}
	if code := status(t, phone, "POST", base+"/api/v1/devices/pair", `{"code":"AAAA-BBBB","name":"Phone"}`); code != 403 {
		t.Errorf("a wrong code = %d", code)
	}
	var paired struct {
		Token  string
		User   api.User
		Server api.ServerInfo
	}
	typed := strings.ToLower(strings.ReplaceAll(pairing.Code, "-", " ")) // as someone might type it
	if code := doJSON(t, phone, "POST", base+"/api/v1/devices/pair", mustJSON(map[string]string{"code": typed, "name": "Test iPhone", "platform": "ios"}), &paired); code != 200 ||
		!strings.HasPrefix(paired.Token, "psd_") || paired.User.Email != adminEmail || paired.Server.ID != info.ID {
		t.Fatalf("pair = %d %+v", code, paired)
	}
	if code := status(t, phone, "POST", base+"/api/v1/devices/pair", mustJSON(map[string]string{"code": pairing.Code, "name": "Again"})); code != 403 {
		t.Errorf("a code used twice = %d", code)
	}
	bearer := map[string]string{"Authorization": "Bearer " + paired.Token, "User-Agent": "PhotosteadApp/1"}
	if resp := do(t, phone, "GET", base+"/api/v1/auth/me", "", bearer); resp.StatusCode != 200 {
		t.Fatalf("the device's token = %d", resp.StatusCode)
	}
	if resp := do(t, phone, "GET", base+"/api/v1/timeline/buckets", "", map[string]string{"Authorization": "Bearer psd_nope"}); resp.StatusCode != 401 {
		t.Errorf("a made-up token = %d", resp.StatusCode)
	}

	// It can upload, as the Capture extension does.
	if resp := do(t, phone, "POST", base+"/api/v1/uploads/check", `{"sha256":[]}`, bearer); resp.StatusCode != 200 {
		t.Errorf("the device's upload check = %d", resp.StatusCode)
	}

	// From away, through Funnel, with no Tailscale on the phone: its own token reaches the API as at
	// home, but for new ways in. Anything else there is share links and guests, as before.
	shareOnly := httptest.NewServer(api.PublicOnly(h))
	defer shareOnly.Close()
	internet := httptest.NewServer(api.PairedAway(h, api.PublicOnly(h), s.Q))
	defer internet.Close()
	if resp := do(t, phone, "GET", shareOnly.URL+"/api/v1/server", "", nil); resp.StatusCode != 404 {
		t.Errorf("with devices kept home, which server this is, from the internet = %d (the phone would take it for its own)", resp.StatusCode)
	}
	var away api.ServerInfo
	getJSON(t, phone, internet.URL+"/api/v1/server", &away)
	if away.ID != info.ID {
		t.Errorf("which server this is, from away = %+v", away)
	}
	for path, want := range map[string]int{"/api/v1/auth/me": 200, "/api/v1/timeline/buckets": 200, "/api/v1/server/addresses": 200, "/api/v1/docs": 404, "/mcp": 404} {
		if resp := do(t, phone, "GET", internet.URL+path, "", bearer); resp.StatusCode != want {
			t.Errorf("the device from away, GET %s = %d (want %d)", path, resp.StatusCode, want)
		}
	}
	if resp := do(t, phone, "POST", internet.URL+"/api/v1/uploads/check", `{"sha256":[]}`, bearer); resp.StatusCode != 200 {
		t.Errorf("the device's upload check from away = %d", resp.StatusCode)
	}
	for path, body := range map[string]string{"/api/v1/devices/pairing": "", "/api/v1/remote/funnel": `{"on":false}`,
		"/api/v1/auth/login": `{"email":"` + adminEmail + `","password":"` + adminPass + `"}`} {
		if resp := do(t, phone, "POST", internet.URL+path, body, bearer); resp.StatusCode != 403 {
			t.Errorf("the device from away, POST %s = %d (want 403)", path, resp.StatusCode)
		}
	}
	if resp := do(t, phone, "GET", internet.URL+"/api/v1/auth/me", "", map[string]string{"Authorization": "Bearer psd_nope"}); resp.StatusCode != 401 {
		t.Errorf("a made-up token from away = %d", resp.StatusCode)
	}
	if resp := do(t, phone, "GET", internet.URL+"/api/v1/server/addresses", "", nil); resp.StatusCode != 404 {
		t.Errorf("no token from away = %d", resp.StatusCode)
	}
	if code := status(t, c, "GET", internet.URL+"/api/v1/server/addresses", ""); code != 404 {
		t.Errorf("a household member's browser from away = %d", code)
	}

	var signed struct{ Sessions []api.SignedIn }
	getJSON(t, c, base+"/api/v1/auth/sessions", &signed)
	var device *api.SignedIn
	current := 0
	for i, x := range signed.Sessions {
		if x.Device == "Test iPhone" {
			device = &signed.Sessions[i]
		}
		if x.Current {
			current++
		}
	}
	if len(signed.Sessions) != 2 || device == nil || device.Platform != "ios" || device.UserAgent != "PhotosteadApp/1" || current != 1 {
		t.Fatalf("sessions = %+v", signed.Sessions)
	}
	if code := status(t, c, "DELETE", base+"/api/v1/auth/sessions/"+device.ID.String(), ""); code != 204 {
		t.Errorf("ending the device's session = %d", code)
	}
	if resp := do(t, phone, "GET", base+"/api/v1/auth/me", "", bearer); resp.StatusCode != 401 {
		t.Errorf("after it was signed out, the device = %d", resp.StatusCode)
	}
	if resp := do(t, phone, "GET", internet.URL+"/api/v1/timeline/buckets", "", bearer); resp.StatusCode != 401 {
		t.Errorf("after it was signed out, the device from away = %d", resp.StatusCode)
	}

	// Remote access: no Tailscale node in this server; and only the admin may turn it on.
	var rem struct {
		Available bool
		Tailscale struct{ Status, Hostname string }
	}
	getJSON(t, c, base+"/api/v1/remote", &rem)
	if rem.Available || rem.Tailscale.Status != "off" || rem.Tailscale.Hostname != "photostead" {
		t.Errorf("remote = %+v", rem)
	}
	if code := status(t, c, "POST", base+"/api/v1/remote/tailscale", `{"on":true}`); code != 409 {
		t.Errorf("turning on a node this server can't run = %d", code)
	}
	member, err := s.Q.CreateUser(ctx, db.CreateUserParams{Email: "member@example.com", Username: "member", DisplayName: "Member", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetPassword(ctx, s.Q, member, "a long member password"); err != nil {
		t.Fatal(err)
	}
	m := browser()
	status(t, m, "POST", base+"/api/v1/auth/login", `{"email":"member@example.com","password":"a long member password"}`)
	if code := status(t, m, "POST", base+"/api/v1/remote/tailscale", `{"on":true}`); code != 403 {
		t.Errorf("a member turning on Tailscale = %d", code)
	}
}

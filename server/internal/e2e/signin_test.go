package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/auth"
)

// A server with just its admin, no library, and single sign-on pointed at issuer (if any).
func signInServer(t *testing.T, ctx context.Context, issuer string, newAccounts bool) (string, func() *http.Client) {
	t.Helper()
	s := tempDatabase(t, ctx)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Bootstrap(ctx, s, adminEmail, adminPass); err != nil {
		t.Fatal(err)
	}
	opts := api.Options{Store: s}
	if issuer != "" {
		opts.OIDC = &auth.OIDC{Config: auth.OIDCConfig{Issuer: issuer, ClientID: "photostead", ClientSecret: "test-secret", Name: "Test SSO", NewAccounts: newAccounts}}
	}
	h, _ := api.Handler(opts)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	browser := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return srv.URL, browser
}

func signIn(t *testing.T, c *http.Client, url string) (code int, r api.LoginResult) {
	t.Helper()
	code = doJSON(t, c, "POST", url+"/api/v1/auth/login", `{"email":"`+adminEmail+`","password":"`+adminPass+`"}`, &r)
	return code, r
}

// Two-step sign-in: turned on with a code from the app; then a password alone isn't enough, a code
// works once, a recovery code works once, and it can be turned off.
func TestTwoStep(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	url, browser := signInServer(t, ctx, "", false)
	c := browser()
	if code, r := signIn(t, c, url); code != 200 || r.User == nil {
		t.Fatalf("sign-in = %d %+v", code, r)
	}
	if code := status(t, c, "POST", url+"/api/v1/auth/two-step/setup", `{"password":"wrong"}`); code != 403 {
		t.Errorf("setup with the wrong password = %d", code)
	}
	var setup struct{ Secret, URI, QR string }
	if code := doJSON(t, c, "POST", url+"/api/v1/auth/two-step/setup", mustJSON(map[string]string{"password": adminPass}), &setup); code != 200 ||
		!strings.HasPrefix(setup.URI, "otpauth://totp/Photostead") || !strings.HasPrefix(setup.QR, "data:image/png;base64,") {
		t.Fatalf("setup = %d %+v", code, setup)
	}
	codeAt := func(at time.Time) string {
		c, err := totp.GenerateCode(setup.Secret, at)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if code := status(t, c, "POST", url+"/api/v1/auth/two-step/confirm", `{"code":"000000"}`); code != 403 {
		t.Errorf("confirming with a wrong code = %d", code)
	}
	var rec struct{ RecoveryCodes []string }
	if code := doJSON(t, c, "POST", url+"/api/v1/auth/two-step/confirm", mustJSON(map[string]string{"code": codeAt(time.Now())}), &rec); code != 200 || len(rec.RecoveryCodes) != 10 {
		t.Fatalf("confirm = %d %d codes", code, len(rec.RecoveryCodes))
	}

	// A password alone gives a ticket, not a session.
	b := browser()
	code, r := signIn(t, b, url)
	if code != 200 || r.User != nil || !r.TwoStep || r.Ticket == "" {
		t.Fatalf("sign-in with two-step on = %d %+v", code, r)
	}
	if code := status(t, b, "GET", url+"/api/v1/auth/me", ""); code != 401 {
		t.Errorf("signed in before the code = %d", code)
	}
	second := func(ticket, code string) int {
		return status(t, b, "POST", url+"/api/v1/auth/login/two-step", mustJSON(map[string]string{"ticket": ticket, "code": code}))
	}
	if got := second(r.Ticket, "123456"); got != 403 {
		t.Errorf("a wrong code = %d", got)
	}
	if got := second(r.Ticket, codeAt(time.Now())); got != 403 { // used to confirm: not again
		t.Errorf("a code used already = %d", got)
	}
	if got := second(r.Ticket, codeAt(time.Now().Add(30*time.Second))); got != 200 {
		t.Fatalf("the next code = %d", got)
	}
	if code := status(t, b, "GET", url+"/api/v1/auth/me", ""); code != 200 {
		t.Errorf("after the code = %d", code)
	}

	// A recovery code, once.
	_, r = signIn(t, browser(), url)
	if got := second(r.Ticket, strings.ToUpper(rec.RecoveryCodes[3])); got != 200 {
		t.Errorf("a recovery code = %d", got)
	}
	_, r = signIn(t, browser(), url)
	if got := second(r.Ticket, rec.RecoveryCodes[3]); got != 403 {
		t.Errorf("a recovery code again = %d", got)
	}
	var sec api.Security
	getJSON(t, c, url+"/api/v1/auth/security", &sec)
	if !sec.TwoStep || sec.RecoveryLeft != 9 || !sec.HasPassword {
		t.Errorf("security = %+v", sec)
	}

	// Off, with the password.
	if code := status(t, c, "POST", url+"/api/v1/auth/two-step/off", mustJSON(map[string]string{"password": adminPass})); code != 204 {
		t.Errorf("off = %d", code)
	}
	if code, r := signIn(t, browser(), url); code != 200 || r.User == nil {
		t.Errorf("sign-in after turning it off = %d %+v", code, r)
	}
}

// A software authenticator: one P-256 key, "none" attestation, as a phone or security key would answer.
type authenticator struct {
	key    *ecdsa.PrivateKey
	id     []byte
	rpID   string
	origin string
	user   []byte
	count  uint32
}

var b64 = base64.RawURLEncoding

func (a *authenticator) authData(attested bool) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	flags := byte(0x05) // user present, user verified
	if attested {
		flags |= 0x40
	}
	d := append(rp[:], flags)
	d = binary.BigEndian.AppendUint32(d, a.count)
	if attested {
		d = append(d, make([]byte, 16)...) // AAGUID
		d = binary.BigEndian.AppendUint16(d, uint16(len(a.id)))
		d = append(d, a.id...)
		x, y := a.key.X.FillBytes(make([]byte, 32)), a.key.Y.FillBytes(make([]byte, 32))
		cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		d = append(d, cose...)
	}
	return d
}

func (a *authenticator) clientData(kind, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	return b
}

// create answers navigator.credentials.create().
func (a *authenticator) create(t *testing.T, options json.RawMessage) json.RawMessage {
	var o struct {
		PublicKey struct {
			Challenge string
			RP        struct{ ID string }
			User      struct{ ID string }
		}
	}
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatal(err)
	}
	a.rpID = o.PublicKey.RP.ID
	a.user, _ = b64.DecodeString(o.PublicKey.User.ID)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(true)})
	out, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(a.id), "rawId": b64.EncodeToString(a.id), "type": "public-key",
		"response": map[string]string{"clientDataJSON": b64.EncodeToString(a.clientData("webauthn.create", o.PublicKey.Challenge)), "attestationObject": b64.EncodeToString(att)}})
	return out
}

// get answers navigator.credentials.get(), signing the challenge.
func (a *authenticator) get(t *testing.T, options json.RawMessage) json.RawMessage {
	var o struct{ PublicKey struct{ Challenge string } }
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatal(err)
	}
	a.count++
	ad, cd := a.authData(false), a.clientData("webauthn.get", o.PublicKey.Challenge)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(a.id), "rawId": b64.EncodeToString(a.id), "type": "public-key",
		"response": map[string]string{"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(ad),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(a.user)}})
	return out
}

// Passkeys: one is added (with the password asked again) and signs in with nothing typed; a
// passkey for another address doesn't; removed, it no longer works.
func TestPasskeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	url, browser := signInServer(t, ctx, "", false)
	origin := strings.Replace(url, "127.0.0.1", "localhost", 1)
	hdr := map[string]string{"Origin": origin, "Content-Type": "application/json"}
	post := func(c *http.Client, path string, body any, out any) int {
		t.Helper()
		resp := do(t, c, "POST", url+path, mustJSON(body), hdr)
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	c := browser()
	signIn(t, c, url)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	a := &authenticator{key: key, id: []byte("test-passkey-0001"), origin: origin}

	type opts struct {
		Flow    string
		Options json.RawMessage
	}
	var begin opts
	if code := post(c, "/api/v1/auth/passkeys/begin", map[string]string{"password": "nope"}, nil); code != 403 {
		t.Errorf("adding a passkey with the wrong password = %d", code)
	}
	if code := post(c, "/api/v1/auth/passkeys/begin", map[string]string{"password": adminPass}, &begin); code != 200 {
		t.Fatalf("begin = %d", code)
	}
	var pk api.Passkey
	if code := post(c, "/api/v1/auth/passkeys/finish", map[string]any{"flow": begin.Flow, "name": "Test key", "credential": a.create(t, begin.Options)}, &pk); code != 201 || pk.Name != "Test key" {
		t.Fatalf("finish = %d %+v", code, pk)
	}

	// Signing in: nothing typed.
	b := browser()
	if code := post(b, "/api/v1/auth/passkey/begin", nil, &begin); code != 200 {
		t.Fatalf("sign-in begin = %d", code)
	}
	var me api.User
	if code := post(b, "/api/v1/auth/passkey/finish", map[string]any{"flow": begin.Flow, "credential": a.get(t, begin.Options)}, &me); code != 200 || me.Email != adminEmail {
		t.Fatalf("passkey sign-in = %d %+v", code, me)
	}
	if code := status(t, b, "GET", url+"/api/v1/auth/me", ""); code != 200 {
		t.Errorf("after the passkey = %d", code)
	}
	// The same answer twice (a replay) doesn't.
	if code := post(browser(), "/api/v1/auth/passkey/finish", map[string]any{"flow": begin.Flow, "credential": a.get(t, begin.Options)}, nil); code != 401 {
		t.Errorf("a replayed sign-in = %d", code)
	}

	// Another address's passkey doesn't sign in here.
	other := &authenticator{key: key, id: a.id, origin: "https://evil.example", rpID: "evil.example", user: a.user}
	post(browser(), "/api/v1/auth/passkey/begin", nil, &begin)
	if code := post(browser(), "/api/v1/auth/passkey/finish", map[string]any{"flow": begin.Flow, "credential": other.get(t, begin.Options)}, nil); code != 401 {
		t.Errorf("another address's passkey = %d", code)
	}

	// Removed, it's gone.
	var sec api.Security
	getJSON(t, c, url+"/api/v1/auth/security", &sec)
	if len(sec.Passkeys) != 1 || sec.Passkeys[0].LastUsedAt == nil {
		t.Fatalf("passkeys = %+v", sec.Passkeys)
	}
	if code := status(t, c, "DELETE", url+"/api/v1/auth/passkeys/"+sec.Passkeys[0].ID.String(), ""); code != 204 {
		t.Errorf("remove = %d", code)
	}
	post(browser(), "/api/v1/auth/passkey/begin", nil, &begin)
	if code := post(browser(), "/api/v1/auth/passkey/finish", map[string]any{"flow": begin.Flow, "credential": a.get(t, begin.Options)}, nil); code != 401 {
		t.Errorf("a removed passkey = %d", code)
	}
}

// A stand-in OpenID Connect provider: it signs in whoever the test says, once per code.
type provider struct {
	srv     *httptest.Server
	key     *rsa.PrivateKey
	email   string
	subject string
	nonces  map[string]string // code -> nonce
}

func newProvider(t *testing.T) *provider {
	p := &provider{nonces: map[string]string{}}
	p.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/jwks", "userinfo_endpoint": p.srv.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"}, "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		nonce, ok := p.nonces[r.Form.Get("code")]
		if !ok || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		delete(p.nonces, r.Form.Get("code"))
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		claims, _ := json.Marshal(map[string]any{"iss": p.srv.URL, "sub": p.subject, "aud": "photostead", "exp": time.Now().Add(time.Hour).Unix(),
			"iat": time.Now().Unix(), "nonce": nonce, "email": p.email, "email_verified": true, "name": "Test Person"})
		jws, _ := signer.Sign(claims)
		idt, _ := jws.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idt, "expires_in": 3600})
	})
	return p
}

// signOn goes round the provider as a browser would: start, the provider's page (which signs in
// p.subject straight away), back. It returns where the browser ends up.
func (p *provider) signOn(t *testing.T, c *http.Client, start *http.Request) string {
	t.Helper()
	resp, err := c.Do(start)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	to, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(to.String(), p.srv.URL+"/authorize") {
		t.Fatalf("start went to %q (%d)", resp.Header.Get("Location"), resp.StatusCode)
	}
	q := to.Query()
	code := uuid.NewString()
	p.nonces[code] = q.Get("nonce")
	back, _ := url.Parse(q.Get("redirect_uri"))
	back.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	resp, err = c.Get(back.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Header.Get("Location")
}

// Single sign-on: an account is found by its verified email and linked; the link then holds even if
// the email changes; someone unknown is turned away, or given an account when that's allowed.
func TestSignOn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := newProvider(t)
	base, browser := signInServer(t, ctx, p.srv.URL, false)
	var status struct{ SignOn string }
	getJSON(t, browser(), base+"/api/v1/setup", &status)
	if status.SignOn != "Test SSO" {
		t.Errorf("setup status = %+v", status)
	}
	start := func() *http.Request {
		r, _ := http.NewRequest("GET", base+"/api/v1/auth/oidc/start", nil)
		return r
	}
	me := func(c *http.Client) string {
		var u api.User
		if resp := do(t, c, "GET", base+"/api/v1/auth/me", "", nil); resp.StatusCode == 200 {
			_ = json.NewDecoder(resp.Body).Decode(&u)
		}
		return u.Email
	}

	p.subject, p.email = "sub-1", strings.ToUpper(adminEmail)
	c := browser()
	if to := p.signOn(t, c, start()); to != "/" || me(c) != adminEmail {
		t.Fatalf("signed on to %q as %q", to, me(c))
	}
	p.email = "renamed@example.com" // linked by subject now, whatever the email
	c = browser()
	if p.signOn(t, c, start()); me(c) != adminEmail {
		t.Error("the link didn't hold")
	}

	p.subject, p.email = "sub-2", "stranger@example.com"
	c = browser()
	if to := p.signOn(t, c, start()); !strings.HasPrefix(to, "/?signin=") || me(c) != "" {
		t.Errorf("a stranger went to %q, signed in as %q", to, me(c))
	}

	// The state must come back to the browser that set off.
	c = browser()
	resp, _ := c.Do(start())
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	thief := browser()
	back, _ := url.Parse(loc.Query().Get("redirect_uri"))
	back.RawQuery = "code=x&state=" + loc.Query().Get("state")
	resp, _ = thief.Get(back.String())
	resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Location"), "/?signin=") || me(thief) != "" {
		t.Error("a sign-on finished in another browser")
	}

	// New accounts, when allowed.
	url2, browser2 := signInServer(t, ctx, p.srv.URL, true)
	base = url2
	c = browser2()
	if to := p.signOn(t, c, start()); to != "/" || me(c) != "stranger@example.com" {
		t.Errorf("a new account: went to %q as %q", to, me(c))
	}
}

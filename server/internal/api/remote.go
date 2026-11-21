package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/remote"
	"photostead.com/server/internal/store/db"
)

// Reaching Photostead from away, and the devices signed in to it. A device (a phone app, the
// Capture extension) pairs with a code the web app shows (and puts in a QR code with the server's
// addresses), and gets a session of its own: a bearer token, good for a year from its last use,
// that its owner can end from the web app like any other session.

const (
	devicePrefix = "psd_"
	deviceTTL    = 365 * 24 * time.Hour
	pairTTL      = 10 * time.Minute
)

// ServerInfo says which server this is, so a device can tell it's the same one at another address.
type ServerInfo struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Addresses are where Photostead has been, or can be, reached.
type Addresses struct {
	Home    string `json:"home,omitempty" doc:"On the home network (learned from a visit there)"`
	Tailnet string `json:"tailnet,omitempty" doc:"On the owner's Tailscale network, over HTTPS"`
	Public  string `json:"public,omitempty" doc:"The public address (PHOTOSTEAD_PUBLIC_URL)"`
}

type serverInfoOutput struct{ Body ServerInfo }

type addressesOutput struct{ Body Addresses }

// SignedIn is a browser or device signed in to an account.
type SignedIn struct {
	ID         uuid.UUID `json:"id"`
	Current    bool      `json:"current" doc:"The one asking"`
	Device     string    `json:"device,omitempty" doc:"A paired device's name (none: a browser)"`
	Platform   string    `json:"platform,omitempty"`
	UserAgent  string    `json:"userAgent,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type sessionsOutput struct {
	Body struct {
		Sessions []SignedIn `json:"sessions"`
	}
}

type pairingOutput struct {
	Body struct {
		Code      string    `json:"code" doc:"Type it into the device, or scan the QR code"`
		ExpiresAt time.Time `json:"expiresAt"`
		Link      string    `json:"link" doc:"photostead://pair?… with the code, the server's id and its addresses"`
		QR        string    `json:"qr" doc:"The link as a QR code (PNG data URL)"`
	}
}

type pairInput struct {
	Body struct {
		Code     string `json:"code" minLength:"8" maxLength:"20"`
		Name     string `json:"name" minLength:"1" maxLength:"100" doc:"What to call the device (\"Rich's iPhone\")"`
		Platform string `json:"platform,omitempty" maxLength:"40" doc:"ios, android, extension…"`
	}
	asker asker
}

func (p *pairInput) Resolve(ctx huma.Context) []error {
	p.asker = askerOf(ctx)
	return nil
}

type pairOutput struct {
	Body struct {
		Token     string     `json:"token" doc:"Send as Authorization: Bearer …. Shown this once"`
		User      User       `json:"user"`
		Server    ServerInfo `json:"server"`
		Addresses Addresses  `json:"addresses"`
	}
}

type tailscaleInput struct {
	Body struct {
		On       bool   `json:"on"`
		Hostname string `json:"hostname,omitempty" pattern:"^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)?$" doc:"The node's name on the tailnet (default photostead)"`
		AuthKey  string `json:"authKey,omitempty" maxLength:"200" doc:"A Tailscale auth key, to join without approving in the browser"`
	}
}

type funnelInput struct {
	Body struct {
		On bool `json:"on"`
	}
}

type funnelDevicesInput struct {
	Body struct {
		On bool `json:"on" doc:"Paired devices reach Photostead through Funnel with their own token, with no Tailscale on them"`
	}
}

type remoteOutput struct {
	Body struct {
		Tailscale remote.TailscaleState `json:"tailscale"`
		Available bool                  `json:"available" doc:"Whether this server can run its own Tailscale node"`
		Addresses Addresses             `json:"addresses"`
	}
}

// tailscaleSetting is what's kept of the Tailscale node: whether it's on, and its name.
type tailscaleSetting struct {
	On        bool   `json:"on"`
	Hostname  string `json:"hostname"`
	Funnel    bool   `json:"funnel,omitempty"`    // share links on the internet
	NoDevices bool   `json:"noDevices,omitempty"` // paired devices kept to home and the tailnet (they come in through Funnel by default)
}

func readTailscale(ctx context.Context, q *db.Queries) tailscaleSetting {
	t := tailscaleSetting{Hostname: "photostead"}
	if raw, err := q.ServerSetting(ctx, "tailscale"); err == nil {
		_ = json.Unmarshal(raw, &t)
	}
	return t
}

func saveTailscale(ctx context.Context, q *db.Queries, t tailscaleSetting) error {
	raw, _ := json.Marshal(t)
	return q.SetServerSetting(ctx, db.SetServerSettingParams{Key: "tailscale", Value: raw})
}

// TailscaleSetting reads whether the server's Tailscale node should run, as what name, whether
// share links go on the internet through Funnel, and whether paired devices come in there too.
func TailscaleSetting(ctx context.Context, q *db.Queries) (on bool, hostname string, funnel, devices bool) {
	t := readTailscale(ctx, q)
	return t.On, t.Hostname, t.Funnel, !t.NoDevices
}

func (s *server) serverInfo(ctx context.Context) (ServerInfo, error) {
	return readServerInfo(ctx, s.Store.Q)
}

func readServerInfo(ctx context.Context, q *db.Queries) (ServerInfo, error) {
	var v struct{ ID uuid.UUID }
	raw, err := q.ServerSetting(ctx, "server")
	if err != nil {
		return ServerInfo{}, err
	}
	err = json.Unmarshal(raw, &v)
	return ServerInfo{ID: v.ID, Name: "Photostead"}, err
}

// homeAddress is the home-network address last seen, cached so a visit costs a write only when it
// changes.
var homeAddress struct {
	sync.Mutex
	url  string
	when time.Time
}

// noteAddress remembers a signed-in visit's address when it's on the home network (a private IP, or
// a .local/.lan/.home.arpa name), for devices to try first when they're home.
func (s *server) noteAddress(ctx huma.Context) {
	host := ctx.Host()
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	ip := net.ParseIP(name)
	home := (ip != nil && (ip.IsPrivate() && !inTailnet(ip))) ||
		strings.HasSuffix(name, ".local") || strings.HasSuffix(name, ".lan") || strings.HasSuffix(name, ".home.arpa")
	if !home || ctx.Header("X-Forwarded-Host") != "" {
		return
	}
	scheme := "http"
	if ctx.TLS() != nil {
		scheme = "https"
	}
	u := scheme + "://" + host
	homeAddress.Lock()
	defer homeAddress.Unlock()
	if homeAddress.url == u && time.Since(homeAddress.when) < time.Hour {
		return
	}
	raw, _ := json.Marshal(map[string]string{"home": u})
	if s.Store.Q.SetServerSetting(ctx.Context(), db.SetServerSettingParams{Key: "home", Value: raw}) == nil {
		homeAddress.url, homeAddress.when = u, time.Now()
	}
}

var _, tailnet4, _ = net.ParseCIDR("100.64.0.0/10")

func inTailnet(ip net.IP) bool { return tailnet4.Contains(ip) }

func (s *server) addresses(ctx context.Context) Addresses {
	a := Addresses{Public: s.PublicURL, Home: readHome(ctx, s.Store.Q)}
	if s.Tailnet != nil {
		a.Tailnet = s.Tailnet.State(ctx).URL
	}
	return a
}

// readHome is the home-network address last seen (see noteAddress).
func readHome(ctx context.Context, q *db.Queries) string {
	var h struct{ Home string }
	if raw, err := q.ServerSetting(ctx, "home"); err == nil {
		_ = json.Unmarshal(raw, &h)
	}
	return h.Home
}

// pairLink is what a device scans: the code, the server's id, and where to find it.
func pairLink(code string, id uuid.UUID, a Addresses) string {
	q := url.Values{"code": {code}, "server": {id.String()}}
	for _, u := range []string{a.Tailnet, a.Public, a.Home} {
		if u != "" {
			q.Add("url", u)
		}
	}
	return "photostead://pair?" + q.Encode()
}

func qrPNG(text string) (string, error) {
	code, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		return "", err
	}
	if code, err = barcode.Scale(code, 280, 280); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, code); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// newPairCode is eight letters and digits that can't be misread, shown as two fours.
func newPairCode() (string, error) {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:]), nil
}

func normalCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

func (s *server) admin(ctx context.Context) error {
	u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
	if err != nil {
		return err
	}
	if u.Role != "admin" {
		return huma.Error403Forbidden("Only the server's admin can change that.")
	}
	return nil
}

func (s *server) remoteState(ctx context.Context) *remoteOutput {
	out := &remoteOutput{}
	out.Body.Available = s.Tailnet != nil
	out.Body.Addresses = s.addresses(ctx)
	if s.Tailnet != nil {
		out.Body.Tailscale = s.Tailnet.State(ctx)
	} else {
		out.Body.Tailscale = remote.TailscaleState{Status: "off"}
	}
	if out.Body.Tailscale.Hostname == "" {
		out.Body.Tailscale.Hostname = readTailscale(ctx, s.Store.Q).Hostname
	}
	return out
}

func (s *server) registerRemote() {
	huma.Register(s.api, huma.Operation{
		OperationID: "serverInfo", Method: http.MethodGet, Path: "/api/v1/server", Tags: []string{"Devices"},
		Summary: "Which server this is", Description: "Its id stays the same at every address it's reached at.",
	}, func(ctx context.Context, _ *struct{}) (*serverInfoOutput, error) {
		info, err := s.serverInfo(ctx)
		return &serverInfoOutput{Body: info}, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "serverAddresses", Method: http.MethodGet, Path: "/api/v1/server/addresses", Tags: []string{"Devices"},
		Summary: "Where this server can be reached: at home, on the tailnet, in public", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*addressesOutput, error) {
		return &addressesOutput{Body: s.addresses(ctx)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "pairingCode", Method: http.MethodPost, Path: "/api/v1/devices/pairing", Tags: []string{"Devices"},
		Summary:     "A code to pair a device with your account",
		Description: "Good once, for 10 minutes. The link (and its QR code) also carries the server's id and addresses.",
		Security:    sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*pairingOutput, error) {
		code, err := newPairCode()
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(struct{}{})
		if _, err := s.Store.Q.StartFlow(ctx, db.StartFlowParams{TokenHash: auth.TokenHash(normalCode(code)), Kind: "pair",
			UserID: uuid.NullUUID{UUID: viewer(ctx), Valid: true}, Data: raw, Seconds: int32(pairTTL / time.Second)}); err != nil {
			return nil, err
		}
		info, err := s.serverInfo(ctx)
		if err != nil {
			return nil, err
		}
		out := &pairingOutput{}
		out.Body.Code, out.Body.ExpiresAt = code, time.Now().Add(pairTTL)
		out.Body.Link = pairLink(code, info.ID, s.addresses(ctx))
		out.Body.QR, err = qrPNG(out.Body.Link)
		return out, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "pairDevice", Method: http.MethodPost, Path: "/api/v1/devices/pair", Tags: []string{"Devices"},
		Summary:     "Pair a device with a code",
		Description: "A code shown in the web app (good once, for 10 minutes), or one that lasts (made by the admin, for a number of days and devices). Gives the device its token (a session of its own), and the server's id and addresses to keep.",
	}, func(ctx context.Context, in *pairInput) (*pairOutput, error) {
		if !s.tryAllowed(in.asker, false) {
			return nil, huma.Error429TooManyRequests("Too many wrong codes. Wait a few minutes.")
		}
		wrong := func() error {
			s.tryAllowed(in.asker, true)
			return huma.Error403Forbidden("That code isn’t right, or it has run out. Show a new one in Photostead.")
		}
		// A code shown in the web app is spent now; one that lasts counts a device, while it has
		// days and devices left.
		codeHash := auth.TokenHash(normalCode(in.Body.Code))
		var userID uuid.UUID
		if f, err := s.Store.Q.TakeFlow(ctx, db.TakeFlowParams{TokenHash: codeHash, Kind: "pair"}); err == nil && f.UserID.Valid {
			_ = s.Store.Q.EndFlow(ctx, f.ID)
			userID = f.UserID.UUID
		} else if c, err := s.Store.Q.UsePairingCode(ctx, codeHash); err == nil {
			userID = c.UserID
		} else {
			return nil, wrong()
		}
		u, err := s.Store.Q.GetUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		if u.Role == "guest" { // guests come in by their personal link, not a paired device
			return nil, wrong()
		}
		token, hash, err := auth.NewToken()
		if err != nil {
			return nil, err
		}
		token = devicePrefix + token
		hash = auth.TokenHash(token)
		platform := pgtype.Text{String: in.Body.Platform, Valid: in.Body.Platform != ""}
		if _, err := s.Store.Q.CreateDeviceSession(ctx, db.CreateDeviceSessionParams{TokenHash: hash, UserID: u.ID,
			ExpiresAt: time.Now().Add(deviceTTL), Device: pgtype.Text{String: strings.TrimSpace(in.Body.Name), Valid: true}, Platform: platform}); err != nil {
			return nil, err
		}
		info, err := s.serverInfo(ctx)
		if err != nil {
			return nil, err
		}
		out := &pairOutput{}
		out.Body.Token, out.Body.User, out.Body.Server, out.Body.Addresses = token, s.userOf(u), info, s.addresses(ctx)
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "sessions", Method: http.MethodGet, Path: "/api/v1/auth/sessions", Tags: []string{"Devices"},
		Summary: "Where you're signed in: browsers and paired devices", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*sessionsOutput, error) {
		rows, err := s.Store.Q.UserSessions(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		cur, _ := sessionOf(ctx)
		out := &sessionsOutput{}
		out.Body.Sessions = make([]SignedIn, len(rows))
		for i, r := range rows {
			out.Body.Sessions[i] = SignedIn{ID: r.ID, Current: r.ID == cur.id, Device: r.Device.String, Platform: r.Platform.String,
				UserAgent: r.UserAgent.String, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "endSession", Method: http.MethodDelete, Path: "/api/v1/auth/sessions/{id}", Tags: []string{"Devices"},
		Summary: "Sign out a browser or device", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		n, err := s.Store.Q.EndUserSession(ctx, db.EndUserSessionParams{ID: in.ID, UserID: viewer(ctx)})
		if err == nil && n == 0 {
			return nil, huma.Error404NotFound("No such session.")
		}
		return nil, err
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "remoteAccess", Method: http.MethodGet, Path: "/api/v1/remote", Tags: []string{"Devices"},
		Summary: "Reaching Photostead from away: its Tailscale node and its addresses", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*remoteOutput, error) {
		return s.remoteState(ctx), nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setTailscale", Method: http.MethodPost, Path: "/api/v1/remote/tailscale", Tags: []string{"Devices"},
		Summary:     "Turn Photostead's own Tailscale node on or off",
		Description: "On, it joins the tailnet (the first time, approve it at the state's loginUrl, or give an auth key) and serves the app over HTTPS there. Admin only.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *tailscaleInput) (*remoteOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		if s.Tailnet == nil {
			return nil, huma.Error409Conflict("This server can't run a Tailscale node.")
		}
		t := readTailscale(ctx, s.Store.Q)
		if in.Body.Hostname != "" {
			t.Hostname = in.Body.Hostname
		}
		t.On = in.Body.On
		if err := saveTailscale(ctx, s.Store.Q, t); err != nil {
			return nil, err
		}
		if in.Body.On {
			s.Tailnet.SetFunnel(t.Funnel)
			if err := s.Tailnet.Start(t.Hostname, in.Body.AuthKey); err != nil {
				return nil, huma.Error502BadGateway("Tailscale didn't start: " + err.Error())
			}
			time.Sleep(1500 * time.Millisecond) // long enough for the login link to come back, usually
		} else {
			s.Tailnet.Stop()
		}
		return s.remoteState(ctx), nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setFunnel", Method: http.MethodPost, Path: "/api/v1/remote/funnel", Tags: []string{"Devices"},
		Summary:     "Put share links on the internet through Tailscale Funnel, or take them off",
		Description: "Share links and guests answer from the internet, and paired devices with their own token unless that's turned off (setFunnelDevices); signing in on the web stays on the tailnet. Needs the tailnet's policy to allow Funnel. Admin only.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *funnelInput) (*remoteOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		if s.Tailnet == nil {
			return nil, huma.Error409Conflict("This server can't run a Tailscale node.")
		}
		t := readTailscale(ctx, s.Store.Q)
		t.Funnel = in.Body.On
		if err := saveTailscale(ctx, s.Store.Q, t); err != nil {
			return nil, err
		}
		s.Tailnet.SetFunnel(in.Body.On)
		return s.remoteState(ctx), nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setFunnelDevices", Method: http.MethodPost, Path: "/api/v1/remote/funnel/devices", Tags: []string{"Devices"},
		Summary:     "Let paired devices in through Funnel, or keep them to home and the tailnet",
		Description: "On (the default), a paired phone reaches Photostead from anywhere through Funnel with its own token, without Tailscale on it. It can't pair other devices, add sign-in methods or change remote access from there. Admin only.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *funnelDevicesInput) (*remoteOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		if s.Tailnet == nil {
			return nil, huma.Error409Conflict("This server can't run a Tailscale node.")
		}
		t := readTailscale(ctx, s.Store.Q)
		t.NoDevices = !in.Body.On
		if err := saveTailscale(ctx, s.Store.Q, t); err != nil {
			return nil, err
		}
		s.Tailnet.SetDevices(in.Body.On)
		return s.remoteState(ctx), nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "forgetTailscale", Method: http.MethodPost, Path: "/api/v1/remote/tailscale/forget", Tags: []string{"Devices"},
		Summary: "Take Photostead off the tailnet for good", Description: "Joining again needs approving again. Admin only.", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*remoteOutput, error) {
		if err := s.admin(ctx); err != nil {
			return nil, err
		}
		if s.Tailnet == nil {
			return nil, huma.Error409Conflict("This server can't run a Tailscale node.")
		}
		t := readTailscale(ctx, s.Store.Q)
		t.On, t.Funnel = false, false
		if err := saveTailscale(ctx, s.Store.Q, t); err != nil {
			return nil, err
		}
		if err := s.Tailnet.Forget(ctx); err != nil {
			return nil, err
		}
		return s.remoteState(ctx), nil
	})
}

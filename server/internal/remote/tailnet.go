// Package remote is reaching Photostead from away from home. Built in: Photostead joins the
// owner's Tailscale network as a node of its own (tsnet) and serves the app there over HTTPS with
// a real certificate, at https://<name>.<tailnet>.ts.net. No ports are opened and no domain is
// needed, and it works the same wherever Photostead runs. Any device on the tailnet (a phone with
// the Tailscale app) reaches it from anywhere. The node's state is kept in the data folder, so it
// stays signed in to the tailnet across restarts.
package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

// TailscaleState is what the tailnet node is doing.
type TailscaleState struct {
	Status   string `json:"status" enum:"off,starting,needs-login,running,error" doc:"needs-login: approve it at loginUrl"`
	LoginURL string `json:"loginUrl,omitempty" doc:"Open it, signed in to Tailscale, to let Photostead join the tailnet"`
	URL      string `json:"url,omitempty" doc:"Where Photostead is on the tailnet, once running"`
	Hostname string `json:"hostname"`
	Error    string `json:"error,omitempty"`
	// Funnel: share links reachable from the internet at the same address, and paired devices too
	// when Devices is on.
	Funnel      bool   `json:"funnel"`
	FunnelError string `json:"funnelError,omitempty"`
	Devices     bool   `json:"devices" doc:"Paired devices reach Photostead through Funnel, with no Tailscale on them"`
}

// Tailnet runs Photostead's own Tailscale node, serving Handler over HTTPS on it.
type Tailnet struct {
	Dir     string       // where the node keeps its state
	Handler http.Handler // the app
	Public  http.Handler // what the internet may reach through Funnel: share links, personal links, guests
	Devices http.Handler // the same, and paired devices signed with their own token (when SetDevices is on)
	Log     *slog.Logger

	devices atomic.Bool

	mu        sync.Mutex
	srv       *tsnet.Server
	hostname  string
	err       error
	cancel    context.CancelFunc
	funnel    bool         // wanted
	funnelLn  net.Listener // open
	funnelErr error
}

// Start joins (or rejoins) the tailnet as hostname. It returns at once; State says how it's going.
func (t *Tailnet) Start(hostname, authKey string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.srv != nil {
		if t.hostname == hostname {
			return nil
		}
		t.stopLocked()
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return err
	}
	var last string // tsnet says "go to this link" every few seconds while it waits: once is enough
	srv := &tsnet.Server{Dir: t.Dir, Hostname: hostname, AuthKey: authKey, Logf: func(string, ...any) {},
		UserLogf: func(format string, args ...any) {
			if msg := strings.TrimSpace(fmt.Sprintf(format, args...)); msg != last {
				last = msg
				t.Log.Info("tailscale: " + msg)
			}
		}}
	if err := srv.Start(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.srv, t.hostname, t.err, t.cancel = srv, hostname, nil, cancel
	go t.serve(ctx, srv)
	return nil
}

// serve answers on the tailnet: the app over HTTPS, and plain HTTP sent there.
func (t *Tailnet) serve(ctx context.Context, srv *tsnet.Server) {
	// Wait until the node is in the tailnet (it may need approving first) before asking for a
	// certificate: HTTPS needs the tailnet's name.
	for ctx.Err() == nil {
		if st := t.state(ctx, srv); st.Status == "running" {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	tls, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		t.fail(srv, fmt.Errorf("HTTPS on the tailnet: %w (turn on HTTPS certificates in the Tailscale admin console, under DNS)", err))
		return
	}
	plain, err := srv.Listen("tcp", ":80")
	if err == nil {
		go func() {
			_ = http.Serve(plain, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
			}))
		}()
	}
	t.Log.Info("Photostead is on the tailnet", "url", t.state(ctx, srv).URL)
	t.mu.Lock()
	if t.funnel && t.srv == srv {
		t.openFunnelLocked()
	}
	t.mu.Unlock()
	hs := &http.Server{Handler: t.Handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = hs.Close()
	}()
	if err := hs.Serve(tls); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
		t.fail(srv, err)
	}
}

// SetFunnel opens (or closes) Funnel: the internet reaches Public at the node's address, on the
// same port the tailnet reaches the whole app. It needs the tailnet's policy to allow Funnel.
func (t *Tailnet) SetFunnel(on bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.funnel = on
	switch {
	case on && t.srv != nil && t.funnelLn == nil:
		if t.state(context.Background(), t.srv).Status == "running" {
			t.openFunnelLocked()
		} // else serve() opens it once the node is in
	case !on:
		t.closeFunnelLocked()
	}
}

func (t *Tailnet) openFunnelLocked() {
	ln, err := t.srv.ListenFunnel("tcp", ":443", tsnet.FunnelOnly())
	if err != nil {
		t.funnelErr = err
		t.Log.Warn("tailscale funnel", "err", err)
		return
	}
	t.funnelLn, t.funnelErr = ln, nil
	t.Log.Info("Funnel is open: share links answer from the internet", "paired devices", t.devices.Load())
	hs := &http.Server{Handler: http.HandlerFunc(t.internet), ReadHeaderTimeout: 10 * time.Second, ConnContext: funnelFrom}
	go func() { _ = hs.Serve(ln) }()
}

type funnelFromKey struct{}

// funnelFrom keeps whom a Funnel connection carries. Its RemoteAddr is Tailscale's relay; the relay
// says whom it's from (Tailscale-Ingress-Src, which only Tailscale's relays may send).
func funnelFrom(ctx context.Context, c net.Conn) context.Context {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if fc, ok := c.(*ipn.FunnelConn); ok && fc.Src.IsValid() {
		return context.WithValue(ctx, funnelFromKey{}, fc.Src)
	}
	return ctx
}

// FunnelFrom is the address a request through Funnel came from, as Tailscale's relay says. False
// for a request that didn't come through Funnel.
func FunnelFrom(ctx context.Context) (netip.AddrPort, bool) {
	ap, ok := ctx.Value(funnelFromKey{}).(netip.AddrPort)
	return ap, ok
}

// internet answers Funnel: Devices while paired devices may come in from away, else Public.
func (t *Tailnet) internet(w http.ResponseWriter, r *http.Request) {
	if t.devices.Load() && t.Devices != nil {
		t.Devices.ServeHTTP(w, r)
		return
	}
	t.Public.ServeHTTP(w, r)
}

// SetDevices lets paired devices (a phone with Photostead's app) in through Funnel with their own
// token, or keeps them to the tailnet and home. It takes effect at once, Funnel open or not.
func (t *Tailnet) SetDevices(on bool) { t.devices.Store(on) }

func (t *Tailnet) closeFunnelLocked() {
	if t.funnelLn != nil {
		_ = t.funnelLn.Close()
	}
	t.funnelLn, t.funnelErr = nil, nil
}

func (t *Tailnet) fail(srv *tsnet.Server, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.srv == srv {
		t.err = err
	}
	t.Log.Warn("tailscale", "err", err)
}

// State reports the node: off, waiting to be approved (with the link), or running (with its address).
func (t *Tailnet) State(ctx context.Context) TailscaleState {
	t.mu.Lock()
	srv, hostname, err := t.srv, t.hostname, t.err
	funnel, funnelErr := t.funnelLn != nil, t.funnelErr
	t.mu.Unlock()
	if srv == nil {
		return TailscaleState{Status: "off", Hostname: hostname, Devices: t.devices.Load()}
	}
	st := t.state(ctx, srv)
	st.Devices = t.devices.Load()
	if err != nil {
		st.Status, st.Error = "error", err.Error()
	}
	st.Funnel = funnel
	if funnelErr != nil {
		st.FunnelError = funnelErr.Error()
	}
	return st
}

func (t *Tailnet) state(ctx context.Context, srv *tsnet.Server) TailscaleState {
	st := TailscaleState{Status: "starting", Hostname: srv.Hostname}
	lc, err := srv.LocalClient()
	if err != nil {
		return st
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		return st
	}
	switch s.BackendState {
	case "NeedsLogin", "NeedsMachineAuth":
		st.Status, st.LoginURL = "needs-login", s.AuthURL
	case "Running":
		st.Status = "running"
		if s.Self != nil && s.Self.DNSName != "" {
			st.URL = "https://" + strings.TrimSuffix(s.Self.DNSName, ".")
		}
	}
	return st
}

// Stop leaves the tailnet for now (it stays approved: starting again needs no new approval).
func (t *Tailnet) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
}

func (t *Tailnet) stopLocked() {
	if t.srv == nil {
		return
	}
	t.closeFunnelLocked()
	t.cancel()
	_ = t.srv.Close()
	t.srv, t.err = nil, nil
}

// Forget signs the node out of the tailnet and deletes its state: joining again needs approving
// again (in another tailnet, say).
func (t *Tailnet) Forget(ctx context.Context) error {
	t.mu.Lock()
	srv := t.srv
	t.mu.Unlock()
	if srv != nil {
		if lc, err := srv.LocalClient(); err == nil {
			_ = lc.Logout(ctx)
		}
	}
	t.Stop()
	return os.RemoveAll(t.Dir)
}

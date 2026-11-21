package remote

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"tailscale.com/ipn"
)

// Funnel answers with Devices only while paired devices may come in from away; else Public.
func TestFunnelLetsDevicesInOnlyWhenAllowed(t *testing.T) {
	say := func(s string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, s) })
	}
	tn := &Tailnet{Public: say("public"), Devices: say("devices")}
	got := func() string {
		rec := httptest.NewRecorder()
		tn.internet(rec, httptest.NewRequest(http.MethodGet, "/api/v1/server", nil))
		return rec.Body.String()
	}
	if g := got(); g != "public" {
		t.Errorf("before it's allowed = %s", g)
	}
	tn.SetDevices(true)
	if g := got(); g != "devices" {
		t.Errorf("allowed = %s", g)
	}
	tn.SetDevices(false)
	if g := got(); g != "public" {
		t.Errorf("turned off again = %s", g)
	}
}

// A request through Funnel knows whom the relay carried it from (its RemoteAddr is the relay's);
// one from anywhere else doesn't claim to be from Funnel.
func TestFunnelFrom(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	src := netip.MustParseAddrPort("203.0.113.9:51000")
	ctx := funnelFrom(context.Background(), tls.Server(&ipn.FunnelConn{Conn: a, Src: src}, &tls.Config{}))
	if got, ok := FunnelFrom(ctx); !ok || got != src {
		t.Errorf("through Funnel = %v %v", got, ok)
	}
	if got, ok := FunnelFrom(funnelFrom(context.Background(), tls.Server(a, &tls.Config{}))); ok {
		t.Errorf("not through Funnel = %v", got)
	}
}

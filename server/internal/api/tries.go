package api

import (
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"

	"photostead.com/server/internal/remote"
)

// Wrong pairing codes and share-link passwords are counted by who sends them: ten in ten minutes
// from one person, and a hundred from one connection. Behind a reverse proxy or tunnel on the same
// machine (cloudflared, Caddy, nginx), every request's connection is the proxy's, so the person is
// whom the proxy says it's from, in X-Forwarded-For. Only a proxy Photostead trusts is believed,
// and only the address it added itself: the rightmost one that isn't a trusted proxy too. What's
// left of that is whatever the asker sent. Someone at home connects from a private address and
// could claim anything, so the connection's own count (the hundred) still holds: made-up headers
// can't buy more guesses than that.
//
// CF-Connecting-IP isn't read: through Cloudflare the same address is on the end of
// X-Forwarded-For, and through any other proxy it's whatever the asker sent.

const (
	triesWindow = 10 * time.Minute
	personTries = 10  // wrong tries from one person (one address; IPv6, one /64)
	connTries   = 100 // from one connection's address: a proxy speaks for many people
)

// Proxies are the addresses whose X-Forwarded-For is believed (PHOTOSTEAD_TRUSTED_PROXIES, read by
// ParseProxies). The zero value believes no one.
type Proxies struct {
	private bool           // loopback and private addresses, but not Tailscale's
	ranges  []netip.Prefix // and these
}

// tailnet6 is Tailscale's IPv6 range: private, but a tailnet device isn't a proxy of ours.
var tailnet6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")

// ParseProxies reads PHOTOSTEAD_TRUSTED_PROXIES: addresses and ranges (127.0.0.1, 10.0.0.0/8,
// fd00::/8), with "private" for the default, separated by commas or spaces. Empty is the default:
// loopback and private addresses (a proxy on the same machine, Docker network or home network), but
// not Tailscale's. "off" believes no one.
func ParseProxies(s string) (Proxies, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "":
		return Proxies{private: true}, nil
	case "off":
		return Proxies{}, nil
	}
	var p Proxies
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if f == "private" {
			p.private = true
			continue
		}
		if r, err := netip.ParsePrefix(f); err == nil {
			p.ranges = append(p.ranges, r.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return Proxies{}, fmt.Errorf("%q isn't an address or a range of them (like 10.0.0.0/8)", f)
		}
		a = plain(a)
		p.ranges = append(p.ranges, netip.PrefixFrom(a, a.BitLen()))
	}
	return p, nil
}

func (p Proxies) trusts(a netip.Addr) bool {
	if p.private && (a.IsLoopback() || a.IsPrivate() && !tailnet6.Contains(a)) {
		return true
	}
	for _, r := range p.ranges {
		if r.Contains(a) {
			return true
		}
	}
	return false
}

// plain drops an IPv6 zone and unwraps an IPv4 address written as IPv6 (::ffff:192.0.2.1).
func plain(a netip.Addr) netip.Addr { return a.WithZone("").Unmap() }

// asker is where a request came from, as an input's Resolve reads it, for counting wrong tries.
type asker struct {
	remote    string         // the connection's address (ip:port)
	forwarded []string       // X-Forwarded-For, each as sent
	funnel    netip.AddrPort // through Funnel: whom Tailscale's relay carried it from
}

func askerOf(ctx huma.Context) asker {
	a := asker{remote: ctx.RemoteAddr()}
	ctx.EachHeader(func(name, value string) {
		if strings.EqualFold(name, "X-Forwarded-For") {
			a.forwarded = append(a.forwarded, value)
		}
	})
	a.funnel, _ = remote.FunnelFrom(ctx.Context())
	return a
}

// who is the person asking and the connection they came by, as the keys their wrong tries are
// counted under.
func (p Proxies) who(a asker) (person, conn string) {
	ap, err := netip.ParseAddrPort(a.remote)
	if err != nil { // not ip:port (a test's request): counted as it is
		return a.remote, a.remote
	}
	peer := plain(ap.Addr())
	from := peer
	switch {
	case a.funnel.IsValid():
		// Through Funnel the connection is Tailscale's relay, which says whom it carries; headers
		// are the asker's own.
		from = plain(a.funnel.Addr())
	case p.trusts(peer):
		hops := strings.Split(strings.Join(a.forwarded, ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			h, ok := hopAddr(hops[i])
			if !ok { // not an address: stop at the proxy that sent it
				break
			}
			from = h
			if !p.trusts(h) {
				break
			}
		}
	}
	return triesKey(from), triesKey(peer)
}

// hopAddr reads one X-Forwarded-For entry: an address, perhaps with a port (1.2.3.4:5678,
// [2001:db8::1]:443) or in brackets.
func hopAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")); err == nil {
		return plain(a), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return plain(ap.Addr()), true
	}
	return netip.Addr{}, false
}

// triesKey is an IPv4 address, or an IPv6 address's /64: one home or phone has a whole /64 to
// pick addresses from.
func triesKey(a netip.Addr) string {
	if a.Is6() {
		return netip.PrefixFrom(a, 64).Masked().String()
	}
	return a.String()
}

// tries holds the wrong tries of the last ten minutes, by person and by connection.
type tries struct {
	sync.Mutex
	wrong map[string][]time.Time
}

var wrongTries tries

// allowed says whether someone may try a code or password now; failed counts a wrong one first.
func (t *tries) allowed(person, conn string, failed bool) bool {
	t.Lock()
	defer t.Unlock()
	now := time.Now()
	if failed {
		if t.wrong == nil {
			t.wrong = map[string][]time.Time{}
		}
		if len(t.wrong) > 10000 { // forget everyone with nothing recent
			for k := range t.wrong {
				t.recent(k, now)
			}
		}
		for _, k := range []string{"person " + person, "conn " + conn} {
			t.wrong[k] = append(t.recent(k, now), now)
		}
	}
	return len(t.recent("person "+person, now)) < personTries && len(t.recent("conn "+conn, now)) < connTries
}

// recent keeps a key's wrong tries from the last ten minutes, and forgets the key when there are none.
func (t *tries) recent(k string, now time.Time) []time.Time {
	kept := t.wrong[k][:0]
	for _, at := range t.wrong[k] {
		if now.Sub(at) < triesWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(t.wrong, k)
		return nil
	}
	t.wrong[k] = kept
	return kept
}

// tryAllowed says whether whoever sent a request may try a pairing code or a share link's password
// now; failed counts a wrong one first.
func (s *server) tryAllowed(a asker, failed bool) bool {
	person, conn := s.Proxies.who(a)
	return wrongTries.allowed(person, conn, failed)
}

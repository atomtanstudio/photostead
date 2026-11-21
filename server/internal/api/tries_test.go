package api

import (
	"fmt"
	"net/netip"
	"testing"
)

// Wrong tries are counted by the person asking: the connection's address, or behind a proxy we
// trust, the address that proxy says it's from. Headers from anyone else are ignored.
func TestWhoIsAsking(t *testing.T) {
	stock, _ := ParseProxies("")
	vps, _ := ParseProxies("100.101.102.103")
	for _, c := range []struct {
		name         string
		proxies      Proxies
		asker        asker
		person, conn string
	}{
		{"a phone at home", stock, asker{remote: "192.168.1.50:51234"}, "192.168.1.50", "192.168.1.50"},
		{"from the internet, straight in", stock, asker{remote: "203.0.113.7:4000"}, "203.0.113.7", "203.0.113.7"},
		{"from the internet, claiming another address", stock,
			asker{remote: "203.0.113.7:4000", forwarded: []string{"198.51.100.1"}}, "203.0.113.7", "203.0.113.7"},
		{"through cloudflared on the same machine", stock,
			asker{remote: "127.0.0.1:40000", forwarded: []string{"203.0.113.7"}}, "203.0.113.7", "127.0.0.1"},
		{"through cloudflared on the Docker network, with a made-up address in front", stock,
			asker{remote: "172.18.0.3:5555", forwarded: []string{"6.6.6.6, 203.0.113.7"}}, "203.0.113.7", "172.18.0.3"},
		{"through Caddy, then nginx", stock,
			asker{remote: "127.0.0.1:40000", forwarded: []string{"6.6.6.6, 203.0.113.7, 10.0.0.5"}}, "203.0.113.7", "127.0.0.1"},
		{"through two proxies, each adding a header of its own", stock,
			asker{remote: "127.0.0.1:40000", forwarded: []string{"6.6.6.6, 203.0.113.7", "10.0.0.5"}}, "203.0.113.7", "127.0.0.1"},
		{"a phone at home through a proxy at home", stock,
			asker{remote: "127.0.0.1:40000", forwarded: []string{"192.168.1.50"}}, "192.168.1.50", "127.0.0.1"},
		{"a proxy that sends no header", stock, asker{remote: "127.0.0.1:40000"}, "127.0.0.1", "127.0.0.1"},
		{"a proxy that sends nonsense", stock,
			asker{remote: "127.0.0.1:40000", forwarded: []string{"203.0.113.7, unknown"}}, "127.0.0.1", "127.0.0.1"},
		{"addresses with ports", stock,
			asker{remote: "127.0.0.1:40000", forwarded: []string{"203.0.113.7:5678"}}, "203.0.113.7", "127.0.0.1"},
		{"someone at home claiming an address (the connection's count still holds)", stock,
			asker{remote: "192.168.1.50:51234", forwarded: []string{"203.0.113.99"}}, "203.0.113.99", "192.168.1.50"},
		{"a tailnet device's header isn't believed", stock,
			asker{remote: "100.101.102.103:5000", forwarded: []string{"203.0.113.7"}}, "100.101.102.103", "100.101.102.103"},
		{"nor over the tailnet's IPv6", stock,
			asker{remote: "[fd7a:115c:a1e0::1]:5000", forwarded: []string{"203.0.113.7"}}, "fd7a:115c:a1e0::/64", "fd7a:115c:a1e0::/64"},
		{"through Funnel: whom the relay carried, whatever the headers say", stock,
			asker{remote: "100.100.1.1:443", forwarded: []string{"6.6.6.6"}, funnel: netip.MustParseAddrPort("203.0.113.7:5555")}, "203.0.113.7", "100.100.1.1"},
		{"IPv6 through a proxy: the person's /64", stock,
			asker{remote: "[::1]:40000", forwarded: []string{"2001:db8:aa:bb:1:2:3:4"}}, "2001:db8:aa:bb::/64", "::/64"},
		{"IPv6 with a port, through a proxy", stock,
			asker{remote: "127.0.0.1:40000", forwarded: []string{"[2001:db8:aa:bb::9]:443"}}, "2001:db8:aa:bb::/64", "127.0.0.1"},
		{"IPv6 straight in: another address in the same /64 is the same person", stock,
			asker{remote: "[2001:db8:aa:bb:ffff::2]:5000"}, "2001:db8:aa:bb::/64", "2001:db8:aa:bb::/64"},
		{"IPv4 written as IPv6", stock,
			asker{remote: "[::ffff:127.0.0.1]:40000", forwarded: []string{"203.0.113.7"}}, "203.0.113.7", "127.0.0.1"},
		{"no proxies trusted", Proxies{}, asker{remote: "127.0.0.1:40000", forwarded: []string{"203.0.113.7"}}, "127.0.0.1", "127.0.0.1"},
		{"a proxy on a VPS over the tailnet, when listed", vps,
			asker{remote: "100.101.102.103:5000", forwarded: []string{"203.0.113.7"}}, "203.0.113.7", "100.101.102.103"},
		{"listing one replaces the default", vps,
			asker{remote: "127.0.0.1:40000", forwarded: []string{"203.0.113.7"}}, "127.0.0.1", "127.0.0.1"},
		{"not an address at all", stock, asker{remote: "pipe", forwarded: []string{"203.0.113.7"}}, "pipe", "pipe"},
	} {
		if person, conn := c.proxies.who(c.asker); person != c.person || conn != c.conn {
			t.Errorf("%s: %s via %s, want %s via %s", c.name, person, conn, c.person, c.conn)
		}
	}
}

func TestParseProxies(t *testing.T) {
	p, err := ParseProxies("private, 100.101.102.103 2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{"127.0.0.1": true, "::1": true, "10.1.2.3": true, "172.18.0.3": true, "192.168.1.1": true,
		"fd00::1": true, "100.101.102.103": true, "2001:db8::5": true, "100.101.102.104": false, "fd7a:115c:a1e0::1": false, "203.0.113.7": false} {
		if got := p.trusts(netip.MustParseAddr(addr)); got != want {
			t.Errorf("trusts %s = %v", addr, got)
		}
	}
	if off, _ := ParseProxies("off"); off.trusts(netip.MustParseAddr("127.0.0.1")) {
		t.Error("off trusts loopback")
	}
	if _, err := ParseProxies("10.0.0.0/8, my-proxy"); err == nil {
		t.Error("a name was taken for an address")
	}
}

// Ten wrong tries stop one person; a hundred from one connection stop everyone on it, so made-up
// headers buy no more than that. Ten minutes later they may try again.
func TestWrongTries(t *testing.T) {
	stock, _ := ParseProxies("")
	var tr tries
	wrong := func(a asker) bool {
		person, conn := stock.who(a)
		return tr.allowed(person, conn, true)
	}
	may := func(a asker) bool {
		person, conn := stock.who(a)
		return tr.allowed(person, conn, false)
	}
	via := func(proxy, person string) asker { return asker{remote: proxy + ":40000", forwarded: []string{person}} }

	// Behind cloudflared, one person's ten wrong codes stop them, not the next person.
	for i := 1; i <= personTries; i++ {
		if ok := wrong(via("127.0.0.1", "203.0.113.7")); ok != (i < personTries) {
			t.Fatalf("wrong try %d: allowed = %v", i, ok)
		}
	}
	if may(via("127.0.0.1", "203.0.113.7")) {
		t.Error("allowed after ten wrong tries")
	}
	if !may(via("127.0.0.1", "198.51.100.4")) {
		t.Error("someone else behind the same proxy is stopped too")
	}

	// Someone at home makes up an address for every try: the connection's count stops them at a
	// hundred, and leaves everyone else alone.
	home := "192.168.1.50:51234"
	for i := 0; i < connTries; i++ {
		wrong(asker{remote: home, forwarded: []string{fmt.Sprintf("198.51.%d.%d", i/250, i%250+1)}})
	}
	if may(asker{remote: home, forwarded: []string{"203.0.113.200"}}) || may(asker{remote: home}) {
		t.Error("still allowed after a hundred wrong tries from one connection")
	}
	if !may(asker{remote: "192.168.1.51:40000"}) {
		t.Error("another phone at home is stopped too")
	}

	// An IPv6 address of its own for every try doesn't help either: they're one /64.
	for i := 0; i < personTries; i++ {
		wrong(asker{remote: fmt.Sprintf("[2001:db8:aa:bb::%x]:5000", i+1)})
	}
	if may(asker{remote: "[2001:db8:aa:bb::ffff]:5000"}) {
		t.Error("a new address in the same /64 is allowed")
	}

	// Ten minutes on, everyone may try again, and the old tries are forgotten.
	tr.Lock()
	for k, times := range tr.wrong {
		for i := range times {
			times[i] = times[i].Add(-triesWindow)
		}
		tr.wrong[k] = times
	}
	tr.Unlock()
	if !may(via("127.0.0.1", "203.0.113.7")) || !may(asker{remote: home}) {
		t.Error("still stopped ten minutes on")
	}
	if _, kept := tr.wrong["person 203.0.113.7"]; kept {
		t.Error("tries that ran out are kept")
	}
}

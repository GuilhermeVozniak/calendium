package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// prefixes parses CIDRs for tests (the config package owns the production default list).
func prefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

func trustedProxy(t *testing.T) proxyTrust {
	t.Helper()
	return proxyTrust{enabled: true, nets: prefixes(t,
		"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7")}
}

// TestClientIP mirrors apps/web/lib/client-ip.test.ts: the web tier sees the
// socket peer as the right-most X-Forwarded-For entry; here it is RemoteAddr.
func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trust      bool
		remoteAddr string
		xff        []string
		want       string
	}{
		{"trust off ignores XFF", false, "10.0.0.2:1234", []string{"203.0.113.9"}, "10.0.0.2"},
		{"trust off, no port", false, "203.0.113.5", nil, "203.0.113.5"},
		{"trust off, ipv6 with port", false, "[2001:db8::1]:443", nil, "2001:db8::1"},
		{"trust off, ipv6 canonicalised", false, "[2001:DB8::1]:443", nil, "2001:db8::1"},
		{"trust off, ipv4-mapped peer unmapped", false, "[::ffff:203.0.113.9]:443", nil, "203.0.113.9"},
		{"trust off, unparsable peer", false, "not-an-address", nil, ""},
		{"trust off, behind a proxy every client shares the proxy bucket", false, "172.18.0.5:1", []string{"198.51.100.7"}, "172.18.0.5"},
		{"trusted peer, zero hops", true, "10.0.0.2:1234", nil, "10.0.0.2"},
		{"trusted peer, one hop", true, "10.0.0.2:1234", []string{"203.0.113.9"}, "203.0.113.9"},
		{"trusted peer, three hops", true, "10.0.0.2:1234", []string{"198.51.100.7, 203.0.113.9, 10.0.0.3"}, "203.0.113.9"},
		{"right-most hop trusted walks left", true, "10.0.0.2:1234", []string{"203.0.113.9, 192.168.1.1"}, "203.0.113.9"},
		{"appending proxy: spoofed left entry ignored", true, "172.18.0.5:1", []string{"6.6.6.6, 198.51.100.7"}, "198.51.100.7"},
		{"proxy chain of trusted hops", true, "172.18.0.5:1", []string{"6.6.6.6, 198.51.100.7, 10.1.2.3"}, "198.51.100.7"},
		{"unparsable hop skipped", true, "10.0.0.2:1234", []string{"203.0.113.9, garbage"}, "203.0.113.9"},
		{"unparsable hops skipped (garbage, port)", true, "127.0.0.1:1", []string{"198.51.100.7, garbage, 198.51.100.8:80"}, "198.51.100.7"},
		{"ipv4-mapped ipv6 hop is unmapped", true, "10.0.0.2:1234", []string{"::ffff:203.0.113.9"}, "203.0.113.9"},
		{"ipv4-mapped hops unmapped before matching", true, "10.0.0.2:1234", []string{"::ffff:198.51.100.7, ::ffff:10.0.0.3"}, "198.51.100.7"},
		{"ipv6 behind a loopback proxy", true, "[::1]:1", []string{"2001:db8::7"}, "2001:db8::7"},
		{"hop zone dropped", true, "10.0.0.2:1234", []string{"fe80::1%eth0"}, "fe80::1"},
		{"multiple XFF headers joined in order", true, "10.0.0.2:1234", []string{"198.51.100.7", "203.0.113.9"}, "203.0.113.9"},
		{"untrusted peer with XFF uses peer", true, "203.0.113.50:1234", []string{"198.51.100.7"}, "203.0.113.50"},
		{"ipv6 peer in ULA trusted", true, "[fd00::5]:1234", []string{"2001:db8::9"}, "2001:db8::9"},
		// Every hop trusted (LAN/VPN client behind the proxy): the left-most
		// valid entry, never the proxy's own shared address (web rule).
		{"all hops trusted: left-most valid entry", true, "10.0.0.2:1234", []string{"192.168.1.1, 10.0.0.3"}, "192.168.1.1"},
		{"LAN client behind Caddy gets its own bucket", true, "172.18.0.5:1", []string{"192.168.1.20"}, "192.168.1.20"},
		{"all-trusted chain (VPN, proxy, Caddy)", true, "172.18.0.5:1", []string{"10.8.0.4, 10.0.0.2"}, "10.8.0.4"},
		{"all-trusted chain skips unparsable left-most", true, "172.18.0.5:1", []string{"garbage, 192.168.1.20"}, "192.168.1.20"},
		{"ipv4-mapped LAN client unmapped", true, "[::1]:1", []string{"::ffff:192.168.1.20"}, "192.168.1.20"},
		{"spoofed private left entries never beat an untrusted hop", true, "172.18.0.5:1", []string{"192.168.1.99, 10.0.0.1, 198.51.100.7"}, "198.51.100.7"},
		{"spoofed public and private left entries", true, "172.18.0.5:1", []string{"6.6.6.6, 10.0.0.1, 198.51.100.7, 10.1.2.3"}, "198.51.100.7"},
		// Review Focus 1: a hop with a port / brackets does not parse and is skipped.
		{"hop with port skipped", true, "10.0.0.2:1234", []string{"203.0.113.9, 198.51.100.7:4321"}, "203.0.113.9"},
		{"bracketed hop skipped", true, "10.0.0.2:1234", []string{"203.0.113.9, [2001:db8::1]"}, "203.0.113.9"},
		{"only unparsable hops fall back to peer", true, "10.0.0.2:1234", []string{"198.51.100.7:4321"}, "10.0.0.2"},
		{"trusted peer whose hops are all unparsable", true, "172.18.0.5:1", []string{"garbage, 1.2.3.4:80, [2001:db8::1]"}, "172.18.0.5"},
		{"blank and whitespace hops", true, "10.0.0.2:1234", []string{" 203.0.113.9 , ", ""}, "203.0.113.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := proxyTrust{}
			if tt.trust {
				p = trustedProxy(t)
			}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := p.clientIP(r); got != tt.want {
				t.Fatalf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIPNarrowedCIDRs(t *testing.T) {
	// Mirrors the web "TRUSTED_PROXY_CIDRS narrows the trusted set" case.
	p := proxyTrust{enabled: true, nets: prefixes(t, "203.0.113.1/32")}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.1:1"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := p.clientIP(r); got != "198.51.100.7" {
		t.Fatalf("narrowed trusted proxy: clientIP() = %q", got)
	}
	r.RemoteAddr = "10.0.0.2:1"
	if got := p.clientIP(r); got != "10.0.0.2" {
		t.Fatalf("10.0.0.2 is no longer trusted: clientIP() = %q", got)
	}
}

func TestPeerTrustedRequiresEnabledAndMembership(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	if (proxyTrust{}).peerTrusted(r) {
		t.Fatal("disabled trust must never trust a peer")
	}
	if !trustedProxy(t).peerTrusted(r) {
		t.Fatal("10.0.0.2 is inside the default trusted CIDRs")
	}
	r.RemoteAddr = "203.0.113.5:1234"
	if trustedProxy(t).peerTrusted(r) {
		t.Fatal("203.0.113.5 is not a trusted proxy")
	}
	r.RemoteAddr = "not-an-address"
	if trustedProxy(t).peerTrusted(r) {
		t.Fatal("an unparsable RemoteAddr must not be trusted")
	}
}

func TestRequestBaseURLForwardedOnlyFromTrustedProxy(t *testing.T) {
	tests := []struct {
		name       string
		trust      bool
		remoteAddr string
		tls        bool
		proto      string
		host       string
		want       string
	}{
		{"plain request", false, "203.0.113.5:1", false, "", "", "http://api.example.test"},
		{"tls request", false, "203.0.113.5:1", true, "", "", "https://api.example.test"},
		{"untrusted peer headers ignored", false, "10.0.0.2:1", false, "https", "evil.example", "http://api.example.test"},
		{"trusted peer headers honoured", true, "10.0.0.2:1", false, "https", "app.example.com", "https://app.example.com"},
		{"trusted peer first value wins", true, "10.0.0.2:1", false, "https, http", "app.example.com, internal", "https://app.example.com"},
		{"trusted peer proto lower-cased", true, "10.0.0.2:1", false, "HTTPS", "", "https://api.example.test"},
		{"trusted but untrusted peer address", true, "203.0.113.5:1", false, "https", "evil.example", "http://api.example.test"},
		// Only http|https is ever taken from X-Forwarded-Proto; anything else
		// falls back to the TLS-derived scheme.
		{"trusted peer javascript proto ignored", true, "10.0.0.2:1", false, "javascript", "", "http://api.example.test"},
		{"trusted peer ftp proto ignored over TLS", true, "10.0.0.2:1", true, "ftp", "", "https://api.example.test"},
		{"trusted peer https:x proto ignored", true, "10.0.0.2:1", false, "https:x", "", "http://api.example.test"},
		{"trusted peer http over TLS honoured", true, "10.0.0.2:1", true, "http", "", "http://api.example.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.TrustProxy = tt.trust
			h.deps.TrustedProxyCIDRs = prefixes(t, "10.0.0.0/8")
			s := h.server()
			r := httptest.NewRequest(http.MethodGet, "http://api.example.test/v1/accounts/connect/google", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tt.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			if tt.host != "" {
				r.Header.Set("X-Forwarded-Host", tt.host)
			}
			if got := s.requestBaseURL(r); got != tt.want {
				t.Fatalf("requestBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

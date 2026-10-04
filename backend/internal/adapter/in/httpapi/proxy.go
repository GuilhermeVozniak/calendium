package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// proxyTrust decides which socket peers may speak for the real client
// through X-Forwarded-* (spec decision 1). With enabled=false every header
// is ignored and the socket address is the client. A client that reaches
// the API directly from a trusted CIDR can spoof the headers — the
// loopback publish in docker-compose.yml closes that, and
// TRUSTED_PROXY_CIDRS can be narrowed to the proxy's own address.
//
// The rule is identical to the web tier's (apps/web/lib/client-ip.ts) so
// both tiers key a request to the same client.
type proxyTrust struct {
	enabled bool
	nets    []netip.Prefix
}

// canonicalAddr parses a bare IP (no port, no brackets), unmapping
// IPv4-in-IPv6 and dropping any zone. Anything else is rejected.
func canonicalAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}

// peerAddr parses the socket peer out of r.RemoteAddr (host:port or bare
// host).
func peerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return canonicalAddr(host)
}

func (p proxyTrust) inNets(a netip.Addr) bool {
	for _, n := range p.nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

// peerTrusted reports whether the socket peer is a trusted proxy.
func (p proxyTrust) peerTrusted(r *http.Request) bool {
	if !p.enabled {
		return false
	}
	a, ok := peerAddr(r)
	return ok && p.inNets(a)
}

// clientIP is the canonical rate-limit / log key (Unmap().String()):
//
//   - the socket peer unless it is a trusted proxy;
//   - otherwise X-Forwarded-For is walked right to left, skipping hops that
//     do not parse (ports, brackets, garbage) or are trusted proxies; the
//     first other hop is the client;
//   - no such hop (every hop is trusted: a LAN/VPN client behind the proxy)
//     → the left-most parseable entry, so such clients do not share the
//     proxy's bucket; the peer when no hop parses;
//   - an unparsable peer → "" (one shared bucket, like the web tier).
func (p proxyTrust) clientIP(r *http.Request) string {
	peer, ok := peerAddr(r)
	if !ok {
		return ""
	}
	if !p.enabled || !p.inNets(peer) {
		return peer.String()
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	leftmost := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := canonicalAddr(hops[i])
		if !ok {
			continue
		}
		if !p.inNets(a) {
			return a.String()
		}
		leftmost = a
	}
	return leftmost.String()
}

// firstForwarded returns the first comma-separated value of header h, only
// when the peer is a trusted proxy; "" otherwise.
func (p proxyTrust) firstForwarded(r *http.Request, h string) string {
	if !p.peerTrusted(r) {
		return ""
	}
	v := r.Header.Get(h)
	if v == "" {
		return ""
	}
	first, _, _ := strings.Cut(v, ",")
	return strings.TrimSpace(first)
}

// forwardedProto returns the first X-Forwarded-Proto value, lower-cased,
// only when the peer is a trusted proxy; "" otherwise.
func (p proxyTrust) forwardedProto(r *http.Request) string {
	return strings.ToLower(p.firstForwarded(r, "X-Forwarded-Proto"))
}

// forwardedHost returns the first X-Forwarded-Host value only when the peer
// is a trusted proxy; "" otherwise.
func (p proxyTrust) forwardedHost(r *http.Request) string {
	return p.firstForwarded(r, "X-Forwarded-Host")
}

// clientIP is the server-level accessor every middleware uses.
func (s *server) clientIP(r *http.Request) string { return s.proxy.clientIP(r) }

// requestBaseURL derives the API's public origin (scheme://host) from the
// incoming request: TLS/Host by default, X-Forwarded-Proto/Host only from a
// trusted proxy. Used to build the provider redirect_uri when
// PUBLIC_API_URL is unset.
func (s *server) requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := s.proxy.forwardedProto(r); p != "" {
		scheme = p
	}
	host := r.Host
	if h := s.proxy.forwardedHost(r); h != "" {
		host = h
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// Package netguard is the ONE SSRF predicate shared by every outbound
// adapter that dials user-supplied hosts (icsfeed subscriptions, RFC 8058
// unsubscribe POSTs, Web Push endpoints): an address may be dialled only when it is public
// global unicast and outside every private, loopback, link-local, ULA,
// unspecified, multicast, CGNAT (100.64.0.0/10) and NAT64 (64:ff9b::/96,
// plus the local-use 64:ff9b:1::/48) range. Nothing in Calendium needs
// NAT64, so both prefixes are refused rather than translated and
// re-checked.
package netguard

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

var blocked = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),  // CGNAT, RFC 6598
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64, RFC 6052
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64, RFC 8215
}

// IsPublic reports whether ip may be dialled. IPv4-mapped IPv6 is unmapped
// first so ::ffff:10.0.0.5 is refused like 10.0.0.5.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Control is a net.Dialer.Control hook: it runs after DNS resolution and
// before the connect syscall, so a hostname resolving (or re-resolving) to
// a refused address can never be reached.
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("netguard: %w", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("netguard: could not parse resolved address %q", host)
	}
	if !IsPublic(ip) {
		return fmt.Errorf("netguard: refusing to dial disallowed address %s", ip)
	}
	return nil
}

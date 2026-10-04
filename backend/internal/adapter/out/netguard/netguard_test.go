package netguard

import (
	"net/netip"
	"testing"
)

// TestIsPublic pins every refused range (moved from icsfeed) plus the
// CGNAT and NAT64 additions.
func TestIsPublic(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"93.184.216.34", true},
		{"2606:2800:220:1::1", true},
		{"127.0.0.1", false},
		{"127.8.8.8", false},
		{"::1", false},
		{"10.0.0.5", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"fdab::12", false},
		{"0.0.0.0", false},
		{"::", false},
		{"224.0.0.1", false},
		{"ff02::1", false},
		{"255.255.255.255", false},
		{"::ffff:10.0.0.5", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:93.184.216.34", true},
		// CGNAT 100.64.0.0/10 (RFC 6598)
		{"100.64.0.1", false},
		{"100.127.255.255", false},
		{"::ffff:100.64.0.1", false},
		{"100.128.0.1", true},
		{"100.63.255.255", true},
		// NAT64 64:ff9b::/96 (RFC 6052): the whole prefix is blocked.
		{"64:ff9b::7f00:1", false},
		{"64:ff9b::5db8:d822", false},
		{"64:ff9b:1::1", true},
	}
	for _, tc := range cases {
		if got := IsPublic(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("IsPublic(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
	if IsPublic(netip.Addr{}) {
		t.Error("IsPublic(zero Addr) = true, want false")
	}
}

func TestControl(t *testing.T) {
	for _, addr := range []string{"93.184.216.34:443", "[2606:2800:220:1::1]:443"} {
		if err := Control("tcp", addr, nil); err != nil {
			t.Fatalf("public literal %s refused: %v", addr, err)
		}
	}
	for _, addr := range []string{
		"100.64.0.1:443", "[64:ff9b::7f00:1]:443", "127.0.0.1:1", "[::ffff:127.0.0.1]:443",
		"[fe80::1%25eth0]:443", "[fe80::1%eth0]:443", "not-an-address", "host.example:443",
	} {
		if err := Control("tcp", addr, nil); err == nil {
			t.Errorf("Control(%q) = nil, want refusal", addr)
		}
	}
}

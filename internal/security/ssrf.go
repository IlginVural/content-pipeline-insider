// Package security decides whether an outbound address is safe for the
// platform to connect to. It answers one question — "may we dial this?"
// — and nothing else: no requests are made here, so every rule is a
// pure function and exhaustively testable without a network.
//
// The threat is SSRF. Administrators supply the URLs this service
// calls, and this service runs inside a trusted network, so without
// these checks an administrator could aim it at internal APIs, the
// cloud metadata endpoint, or a database and have the response parsed
// and displayed back to them.
package security

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

var blockedIPv4Ranges = []string{
	"0.0.0.0/8",      // "this network"
	"10.0.0.0/8",     // RFC1918 private
	"100.64.0.0/10",  // carrier-grade NAT
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local — includes 169.254.169.254, the
	// cloud metadata endpoint that hands out IAM
	// credentials to anything that asks
	"172.16.0.0/12",      // RFC1918 private
	"192.0.0.0/24",       // IETF protocol assignments
	"192.0.2.0/24",       // TEST-NET-1
	"192.168.0.0/16",     // RFC1918 private
	"198.18.0.0/15",      // benchmarking
	"198.51.100.0/24",    // TEST-NET-2
	"203.0.113.0/24",     // TEST-NET-3
	"224.0.0.0/4",        // multicast
	"240.0.0.0/4",        // reserved
	"255.255.255.255/32", // broadcast
}

// IPv4-mapped IPv6 (::ffff:127.0.0.1) is deliberately NOT listed here.
// IsBlockedIP normalizes such addresses with To4() before matching, so
// they are judged against the IPv4 ranges above — ::ffff:127.0.0.1 is
// caught by 127.0.0.0/8. Adding ::ffff:0:0/96 to this list looks
// correct but is not: ParseCIDR reduces it to a 4-byte 0.0.0.0 whose
// mask truncates to all zeros, which then matches every IPv4 address
// on earth and blocks the entire internet.
var blockedIPv6Ranges = []string{
	"::/128",        // unspecified
	"::1/128",       // loopback
	"fc00::/7",      // unique local
	"fe80::/10",     // link-local
	"ff00::/8",      // multicast
	"2001:db8::/32", // documentation
}

var blockedNets = parseCIDRs(append(blockedIPv4Ranges, blockedIPv6Ranges...))

func parseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, network, err := net.ParseCIDR(c)
		if err != nil {
			panic("security: bad CIDR constant " + c + ": " + err.Error())
		}
		out = append(out, network)
	}
	return out
}

// Policy carries the exceptions a caller is allowed to make. The zero
// value is the strict production posture, so a caller that forgets to
// build one gets full protection rather than none.
//
// Each field widens exactly one rule. There is deliberately no "disable
// everything" option: an escape hatch that turns off the address checks
// wholesale is indistinguishable from having no checks, and it is the
// setting that ends up in production by accident.
type Policy struct {
	// AllowHTTP permits plaintext http:// targets. Partner APIs are
	// https; this exists for local development against a test server.
	AllowHTTP bool

	// AllowLoopback permits 127.0.0.0/8 and ::1, and nothing else.
	// Link-local stays blocked, so the cloud metadata endpoint at
	// 169.254.169.254 is unreachable even with this set. For local
	// development only.
	AllowLoopback bool
}

// IsBlockedIP reports whether an address is off limits under the strict
// policy. Callers needing a development exception use IsBlockedIPPolicy.
func IsBlockedIP(ip net.IP) bool {
	return IsBlockedIPPolicy(ip, Policy{})
}

func IsBlockedIPPolicy(ip net.IP, policy Policy) bool {
	if ip == nil {
		return true // cannot verify it, so refuse it
	}

	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	// Checked before the block list rather than as a bypass around it:
	// only loopback is exempted, and every other reserved range below
	// still applies.
	if policy.AllowLoopback && ip.IsLoopback() {
		return false
	}

	for _, network := range blockedNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func ValidateURL(u *url.URL, policy Policy) error {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(policy.AllowHTTP && scheme == "http") {
		return fmt.Errorf("%w: %q (https is required)", ErrBlockedScheme, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: URL has no host", ErrBlockedTarget)
	}

	if port := u.Port(); port != "" && !allowedPorts[port] {
		return fmt.Errorf("%w: %s", ErrBlockedPort, port)
	}

	// A literal IP in the URL can be judged immediately, no DNS needed.
	if ip := net.ParseIP(host); ip != nil && IsBlockedIPPolicy(ip, policy) {
		return fmt.Errorf("%w: %s", ErrBlockedTarget, host)
	}
	return nil
}

var allowedPorts = map[string]bool{
	"80": true, "443": true, "8080": true, "8443": true,
}

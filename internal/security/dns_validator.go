package security

import (
	"context"
	"fmt"
	"net"
)

// Resolver is the DNS lookup this package depends on. It is an
// interface purely so tests can supply canned answers — real DNS in a
// unit test would be slow, flaky, and dependent on whoever owns the
// domain today.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// DefaultResolver is Go's standard resolver.
var DefaultResolver Resolver = net.DefaultResolver

// ValidateHost resolves a hostname and rejects it if ANY resolved
// address is blocked.
//
// "Any", not "all", is deliberate. A hostname can carry several A/AAAA
// records, and Go's dialer tries them in order until one connects. If
// evil.com resolves to both a public address and 127.0.0.1, accepting
// it because one address looked fine would mean the connection might
// still land on loopback. One bad address poisons the name.
func ValidateHost(ctx context.Context, resolver Resolver, host string) ([]net.IPAddr, error) {
	// A literal IP needs no lookup — judge it directly.
	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedIP(ip) {
			return nil, fmt.Errorf("%w: %s", ErrBlockedTarget, host)
		}
		return []net.IPAddr{{IP: ip}}, nil
	}

	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDNSFailure, host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: %s resolved to nothing", ErrDNSFailure, host)
	}

	for _, addr := range addrs {
		if IsBlockedIP(addr.IP) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlockedTarget, host, addr.IP)
		}
	}
	return addrs, nil
}

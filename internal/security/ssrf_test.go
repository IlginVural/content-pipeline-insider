package security

import (
	"errors"
	"net"
	"net/url"
	"testing"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
		why     string
	}{
		// The one that matters most: the cloud metadata endpoint hands
		// out IAM credentials to anything that can reach it.
		{"169.254.169.254", true, "cloud metadata"},
		{"169.254.0.1", true, "link-local"},

		{"127.0.0.1", true, "loopback"},
		{"127.1.2.3", true, "loopback range, not just .0.1"},
		{"10.0.0.1", true, "RFC1918"},
		{"172.16.0.1", true, "RFC1918"},
		{"172.31.255.255", true, "RFC1918 upper bound"},
		{"192.168.1.1", true, "RFC1918"},
		{"100.64.0.1", true, "carrier-grade NAT"},
		{"0.0.0.0", true, "this network"},
		{"192.0.0.1", true, "IETF protocol assignments"},
		{"192.0.2.1", true, "TEST-NET-1"},
		{"198.18.0.1", true, "benchmarking"},
		{"198.51.100.1", true, "TEST-NET-2"},
		{"203.0.113.1", true, "TEST-NET-3"},
		{"224.0.0.1", true, "multicast"},
		{"240.0.0.1", true, "reserved"},
		{"255.255.255.255", true, "broadcast"},

		// IPv4-mapped IPv6. ssrf.go deliberately omits ::ffff:0:0/96
		// from the block list and relies on To4() normalization instead;
		// this is the test that the reasoning actually holds.
		{"::ffff:127.0.0.1", true, "IPv4-mapped loopback"},
		{"::ffff:169.254.169.254", true, "IPv4-mapped metadata"},
		{"::ffff:10.0.0.1", true, "IPv4-mapped RFC1918"},

		{"::1", true, "IPv6 loopback"},
		{"::", true, "IPv6 unspecified"},
		{"fc00::1", true, "IPv6 unique local"},
		{"fd12:3456::1", true, "IPv6 unique local, fd prefix"},
		{"fe80::1", true, "IPv6 link-local"},
		{"ff02::1", true, "IPv6 multicast"},
		{"2001:db8::1", true, "IPv6 documentation"},

		// The other half of the contract: ordinary public addresses must
		// still be reachable. A block list that is too broad silently
		// breaks every integration.
		{"8.8.8.8", false, "public DNS"},
		{"1.1.1.1", false, "public DNS"},
		{"93.184.216.34", false, "public web host"},
		{"172.32.0.1", false, "just above the RFC1918 172.16/12 range"},
		{"172.15.255.255", false, "just below the RFC1918 172.16/12 range"},
		{"100.128.0.1", false, "just above the CGNAT 100.64/10 range"},
		{"2606:4700:4700::1111", false, "public IPv6 resolver"},
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test bug: %q is not a parseable IP", tc.ip)
			}
			if got := IsBlockedIP(ip); got != tc.blocked {
				t.Errorf("IsBlockedIP(%s) = %v, want %v (%s)", tc.ip, got, tc.blocked, tc.why)
			}
		})
	}
}

// A nil IP means the address could not be determined. Refusing it is the
// only safe reading — "we don't know where this goes" is not permission.
func TestIsBlockedIPRejectsNil(t *testing.T) {
	if !IsBlockedIP(nil) {
		t.Error("IsBlockedIP(nil) = false, want true")
	}
}

// AllowLoopback is a development convenience, and the whole question is
// how far it reaches. Previously the equivalent option in the fetcher
// abandoned address checking altogether on any validation failure, which
// made 169.254.169.254 reachable — the cloud metadata endpoint, from a
// flag named "allow loopback".
func TestPolicyAllowLoopbackExemptsOnlyLoopback(t *testing.T) {
	policy := Policy{AllowLoopback: true}

	t.Run("loopback becomes reachable", func(t *testing.T) {
		for _, addr := range []string{"127.0.0.1", "127.1.2.3", "::1", "::ffff:127.0.0.1"} {
			if IsBlockedIPPolicy(net.ParseIP(addr), policy) {
				t.Errorf("IsBlockedIPPolicy(%s, AllowLoopback) = true, want false", addr)
			}
		}
	})

	t.Run("everything else stays blocked", func(t *testing.T) {
		stillBlocked := []struct {
			ip  string
			why string
		}{
			{"169.254.169.254", "cloud metadata — the address this must never expose"},
			{"169.254.0.1", "link-local"},
			{"10.0.0.1", "RFC1918"},
			{"172.16.0.1", "RFC1918"},
			{"192.168.1.1", "RFC1918"},
			{"100.64.0.1", "carrier-grade NAT"},
			{"0.0.0.0", "this network"},
			{"224.0.0.1", "multicast"},
			{"fe80::1", "IPv6 link-local"},
			{"fc00::1", "IPv6 unique local"},
			{"::", "IPv6 unspecified"},
		}
		for _, tc := range stillBlocked {
			if !IsBlockedIPPolicy(net.ParseIP(tc.ip), policy) {
				t.Errorf("IsBlockedIPPolicy(%s, AllowLoopback) = false, want true (%s)", tc.ip, tc.why)
			}
		}
	})

	t.Run("nil is still refused", func(t *testing.T) {
		if !IsBlockedIPPolicy(nil, policy) {
			t.Error("IsBlockedIPPolicy(nil, AllowLoopback) = false, want true")
		}
	})

	// The zero Policy is the production posture, so a caller that forgets
	// to build one is protected rather than exposed.
	t.Run("the zero policy blocks loopback", func(t *testing.T) {
		if !IsBlockedIPPolicy(net.ParseIP("127.0.0.1"), Policy{}) {
			t.Error("IsBlockedIPPolicy(127.0.0.1, Policy{}) = false, want true")
		}
	})
}

func TestValidateURL(t *testing.T) {
	cases := []struct {
		name    string
		url     *url.URL
		policy  Policy
		wantErr error
	}{
		{
			name: "https public host",
			url:  &url.URL{Scheme: "https", Host: "api.partner.com"},
		},
		{
			name:    "http rejected by default",
			url:     &url.URL{Scheme: "http", Host: "api.partner.com"},
			wantErr: ErrBlockedScheme,
		},
		{
			name:   "http allowed when opted in",
			url:    &url.URL{Scheme: "http", Host: "api.partner.com"},
			policy: Policy{AllowHTTP: true},
		},
		{
			name:    "ftp rejected",
			url:     &url.URL{Scheme: "ftp", Host: "api.partner.com"},
			wantErr: ErrBlockedScheme,
		},
		{
			name:    "file rejected",
			url:     &url.URL{Scheme: "file", Host: "etc"},
			wantErr: ErrBlockedScheme,
		},
		{
			// url.Parse lowercases the scheme, but a URL built in code
			// need not have, and ValidateURL folds case for that reason.
			name: "uppercase scheme is folded",
			url:  &url.URL{Scheme: "HTTPS", Host: "api.partner.com"},
		},
		{
			name:    "missing host",
			url:     &url.URL{Scheme: "https"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "ssh port rejected",
			url:     &url.URL{Scheme: "https", Host: "api.partner.com:22"},
			wantErr: ErrBlockedPort,
		},
		{
			name:    "postgres port rejected",
			url:     &url.URL{Scheme: "https", Host: "api.partner.com:5432"},
			wantErr: ErrBlockedPort,
		},
		{
			name: "8443 allowed",
			url:  &url.URL{Scheme: "https", Host: "api.partner.com:8443"},
		},
		{
			name: "443 allowed",
			url:  &url.URL{Scheme: "https", Host: "api.partner.com:443"},
		},
		{
			name:    "literal loopback rejected without any DNS",
			url:     &url.URL{Scheme: "https", Host: "127.0.0.1"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "literal metadata address rejected",
			url:     &url.URL{Scheme: "https", Host: "169.254.169.254"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "literal IPv6 loopback rejected",
			url:     &url.URL{Scheme: "https", Host: "[::1]"},
			wantErr: ErrBlockedTarget,
		},
		{
			name: "literal public IP allowed",
			url:  &url.URL{Scheme: "https", Host: "8.8.8.8"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateURL(tc.url, tc.policy)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateURL(%s) = %v, want nil", tc.url, err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateURL(%s) = %v, want %v", tc.url, err, tc.wantErr)
			}
		})
	}
}

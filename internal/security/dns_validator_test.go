package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

// fakeResolver returns canned DNS answers. Real DNS in these tests would
// be slow, flaky, and dependent on whoever owns the domain today — and
// the interesting cases (a public name that also resolves to loopback)
// cannot be arranged with real records at all.
type fakeResolver struct {
	addrs  []string
	err    error
	called int
}

func (f *fakeResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]net.IPAddr, 0, len(f.addrs))
	for _, a := range f.addrs {
		out = append(out, net.IPAddr{IP: net.ParseIP(a)})
	}
	return out, nil
}

func TestValidateHost(t *testing.T) {
	cases := []struct {
		name    string
		addrs   []string
		wantErr error
	}{
		{
			name:  "all public addresses accepted",
			addrs: []string{"93.184.216.34", "8.8.8.8"},
		},
		{
			// The rule the package documents as "any, not all". Go's
			// dialer tries addresses in order, so a name that resolves
			// to both a public address and loopback can still land on
			// loopback. One bad address has to poison the whole name.
			name:    "public address paired with loopback is rejected",
			addrs:   []string{"93.184.216.34", "127.0.0.1"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "loopback listed first is rejected",
			addrs:   []string{"127.0.0.1", "93.184.216.34"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "public address paired with metadata endpoint is rejected",
			addrs:   []string{"93.184.216.34", "169.254.169.254"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "public address paired with RFC1918 is rejected",
			addrs:   []string{"93.184.216.34", "10.1.2.3"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "IPv6 loopback among public addresses is rejected",
			addrs:   []string{"93.184.216.34", "::1"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "no records at all",
			addrs:   nil,
			wantErr: ErrDNSFailure,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &fakeResolver{addrs: tc.addrs}
			_, err := ValidateHost(context.Background(), resolver, "partner.example.com", Policy{})

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateHost() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateHost() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateHostDNSFailure(t *testing.T) {
	resolver := &fakeResolver{err: errors.New("nxdomain")}
	if _, err := ValidateHost(context.Background(), resolver, "nope.example.com", Policy{}); !errors.Is(err, ErrDNSFailure) {
		t.Fatalf("ValidateHost() = %v, want %v", err, ErrDNSFailure)
	}
}

// A literal IP is judged directly. Resolving it would be pointless and,
// with a hostile resolver, an extra thing to get wrong.
func TestValidateHostLiteralIPSkipsDNS(t *testing.T) {
	t.Run("public literal", func(t *testing.T) {
		resolver := &fakeResolver{addrs: []string{"127.0.0.1"}}
		addrs, err := ValidateHost(context.Background(), resolver, "8.8.8.8", Policy{})
		if err != nil {
			t.Fatalf("ValidateHost() = %v, want nil", err)
		}
		if len(addrs) != 1 || !addrs[0].IP.Equal(net.ParseIP("8.8.8.8")) {
			t.Fatalf("ValidateHost() = %v, want the literal address back", addrs)
		}
		if resolver.called != 0 {
			t.Errorf("resolver called %d times for a literal IP, want 0", resolver.called)
		}
	})

	t.Run("blocked literal", func(t *testing.T) {
		resolver := &fakeResolver{}
		_, err := ValidateHost(context.Background(), resolver, "169.254.169.254", Policy{})
		if !errors.Is(err, ErrBlockedTarget) {
			t.Fatalf("ValidateHost() = %v, want %v", err, ErrBlockedTarget)
		}
		if resolver.called != 0 {
			t.Errorf("resolver called %d times for a literal IP, want 0", resolver.called)
		}
	})
}

func TestValidateRedirectHopLimit(t *testing.T) {
	target := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.partner.com"}}

	for hops := 0; hops < MaxRedirects; hops++ {
		via := make([]*http.Request, hops)
		resolver := &fakeResolver{addrs: []string{"93.184.216.34"}}
		if err := ValidateRedirect(context.Background(), resolver, target, via, Policy{}); err != nil {
			t.Fatalf("ValidateRedirect() with %d prior hops = %v, want nil", hops, err)
		}
	}

	via := make([]*http.Request, MaxRedirects)
	resolver := &fakeResolver{addrs: []string{"93.184.216.34"}}
	err := ValidateRedirect(context.Background(), resolver, target, via, Policy{})
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("ValidateRedirect() with %d prior hops = %v, want %v", MaxRedirects, err, ErrTooManyRedirects)
	}
}

// The bypass this function exists to close: an innocent-looking public
// host answering 302 Location: http://169.254.169.254/. Validating only
// the original URL would let it through.
func TestValidateRedirectRevalidatesTarget(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		addrs   []string
		wantErr error
	}{
		{
			name:    "redirect to a literal metadata address",
			host:    "169.254.169.254",
			wantErr: ErrBlockedTarget,
		},
		{
			name:    "redirect to a hostname that resolves to loopback",
			host:    "innocent.example.com",
			addrs:   []string{"127.0.0.1"},
			wantErr: ErrBlockedTarget,
		},
		{
			name:  "redirect to a genuinely public host",
			host:  "api.partner.com",
			addrs: []string{"93.184.216.34"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &http.Request{URL: &url.URL{Scheme: "https", Host: tc.host}}
			resolver := &fakeResolver{addrs: tc.addrs}

			err := ValidateRedirect(context.Background(), resolver, req, nil, Policy{})
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateRedirect() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateRedirect() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateTarget(t *testing.T) {
	t.Run("scheme is rejected before any DNS happens", func(t *testing.T) {
		resolver := &fakeResolver{addrs: []string{"93.184.216.34"}}
		u := &url.URL{Scheme: "http", Host: "api.partner.com"}

		if err := ValidateTarget(context.Background(), resolver, u, Policy{}); !errors.Is(err, ErrBlockedScheme) {
			t.Fatalf("ValidateTarget() = %v, want %v", err, ErrBlockedScheme)
		}
		if resolver.called != 0 {
			t.Errorf("resolver called %d times, want 0 — the scheme check should short-circuit", resolver.called)
		}
	})

	t.Run("resolves and accepts a public host", func(t *testing.T) {
		resolver := &fakeResolver{addrs: []string{"93.184.216.34"}}
		u := &url.URL{Scheme: "https", Host: "api.partner.com"}

		if err := ValidateTarget(context.Background(), resolver, u, Policy{}); err != nil {
			t.Fatalf("ValidateTarget() = %v, want nil", err)
		}
		if resolver.called != 1 {
			t.Errorf("resolver called %d times, want 1", resolver.called)
		}
	})
}

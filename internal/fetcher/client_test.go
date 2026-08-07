package fetcher

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"content-pipeline-insider/internal/security"
)

func TestOptionsPolicy(t *testing.T) {
	t.Run("zero options are the strict posture", func(t *testing.T) {
		p := Options{}.policy()
		if p.AllowHTTP || p.AllowLoopback {
			t.Errorf("Options{}.policy() = %+v, want everything refused", p)
		}
	})

	t.Run("each option widens exactly one rule", func(t *testing.T) {
		if p := (Options{AllowHTTP: true}).policy(); !p.AllowHTTP || p.AllowLoopback {
			t.Errorf("AllowHTTP alone = %+v, want only AllowHTTP set", p)
		}
		if p := (Options{AllowLoopback: true}).policy(); p.AllowHTTP || !p.AllowLoopback {
			t.Errorf("AllowLoopback alone = %+v, want only AllowLoopback set", p)
		}
	})
}

// The address checks run before any connection is attempted, so these
// need no network. A blocked target must be refused by validation rather
// than by the dial failing.
func TestDoRefusesBlockedTargets(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		opts    Options
		wantErr error
	}{
		{
			name:    "cloud metadata endpoint",
			url:     "https://169.254.169.254/latest/meta-data/",
			wantErr: security.ErrBlockedTarget,
		},
		{
			name:    "loopback",
			url:     "https://127.0.0.1/internal",
			wantErr: security.ErrBlockedTarget,
		},
		{
			name:    "private network",
			url:     "https://10.1.2.3/admin",
			wantErr: security.ErrBlockedTarget,
		},
		{
			name:    "plaintext http by default",
			url:     "http://api.partner.com/products",
			wantErr: security.ErrBlockedScheme,
		},
		{
			name:    "database port",
			url:     "https://api.partner.com:5432/",
			wantErr: security.ErrBlockedPort,
		},
		{
			// The property that makes AllowLoopback safe to expose: it
			// widens loopback and nothing else. Before, the equivalent
			// option abandoned address checking altogether, which put
			// this exact address within reach.
			name:    "metadata endpoint stays blocked even with AllowLoopback",
			url:     "https://169.254.169.254/latest/meta-data/",
			opts:    Options{AllowLoopback: true, AllowHTTP: true},
			wantErr: security.ErrBlockedTarget,
		},
		{
			name:    "private network stays blocked with AllowLoopback",
			url:     "https://10.1.2.3/admin",
			opts:    Options{AllowLoopback: true},
			wantErr: security.ErrBlockedTarget,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := New(tc.opts)
			req, err := http.NewRequest(http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatalf("NewRequest() = %v", err)
			}

			_, err = f.Do(context.Background(), req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Do(%s) = %v, want %v", tc.url, err, tc.wantErr)
			}
		})
	}
}

// AllowLoopback has to actually permit loopback, or it is a flag that
// does nothing. Port 8443 is used because the allowed-port list still
// applies — widening the address rule does not widen the port rule.
func TestDoAllowsLoopbackWhenEnabled(t *testing.T) {
	f := New(Options{AllowLoopback: true, AllowHTTP: true})
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8443/local", nil)
	if err != nil {
		t.Fatalf("NewRequest() = %v", err)
	}

	// Nothing is listening, so this must fail at the connection rather
	// than at validation. Any security sentinel here means the option
	// did not take effect.
	_, err = f.Do(context.Background(), req)
	if errors.Is(err, security.ErrBlockedTarget) || errors.Is(err, security.ErrBlockedScheme) {
		t.Fatalf("Do() = %v, want loopback permitted when AllowLoopback is set", err)
	}
}

// checkRedirect runs the full check on every hop. It previously verified
// only scheme, port and literal address, leaving resolution to the
// dialer — so a blocked redirect surfaced as a connection error instead
// of a blocked target.
func TestCheckRedirect(t *testing.T) {
	f := New(Options{})

	newHop := func(rawURL string) *http.Request {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatalf("NewRequest() = %v", err)
		}
		return req
	}

	t.Run("redirect to the metadata endpoint is refused", func(t *testing.T) {
		err := f.checkRedirect(newHop("https://169.254.169.254/"), nil)
		if !errors.Is(err, security.ErrBlockedTarget) {
			t.Fatalf("checkRedirect() = %v, want %v", err, security.ErrBlockedTarget)
		}
	})

	t.Run("redirect to loopback is refused", func(t *testing.T) {
		err := f.checkRedirect(newHop("https://127.0.0.1/internal"), nil)
		if !errors.Is(err, security.ErrBlockedTarget) {
			t.Fatalf("checkRedirect() = %v, want %v", err, security.ErrBlockedTarget)
		}
	})

	t.Run("redirect downgrading to http is refused", func(t *testing.T) {
		err := f.checkRedirect(newHop("http://api.partner.com/"), nil)
		if !errors.Is(err, security.ErrBlockedScheme) {
			t.Fatalf("checkRedirect() = %v, want %v", err, security.ErrBlockedScheme)
		}
	})

	// One sentinel per condition. Two packages defining ErrBlockedTarget
	// meant errors.Is answered differently depending on which one the
	// caller imported, and guardedDial returned one of each by path.
	t.Run("hop limit reports the security sentinel", func(t *testing.T) {
		via := make([]*http.Request, security.MaxRedirects)
		err := f.checkRedirect(newHop("https://api.partner.com/"), via)
		if !errors.Is(err, security.ErrTooManyRedirects) {
			t.Fatalf("checkRedirect() = %v, want %v", err, security.ErrTooManyRedirects)
		}
	})
}

func TestCheckJSON(t *testing.T) {
	cases := []struct {
		name        string
		resp        *Response
		wantErr     error
		description string
	}{
		{
			name: "200 with json",
			resp: &Response{StatusCode: 200, ContentType: "application/json"},
		},
		{
			name: "200 with a json suffix type",
			resp: &Response{StatusCode: 200, ContentType: "application/vnd.partner+json"},
		},
		{
			name: "200 with charset parameters",
			resp: &Response{StatusCode: 200, ContentType: "application/json; charset=utf-8"},
		},
		{
			// A partner that omits the header is common enough that
			// refusing would break real integrations.
			name: "200 with no content type",
			resp: &Response{StatusCode: 200},
		},
		{
			name:    "401 with a perfectly valid error document",
			resp:    &Response{StatusCode: 401, ContentType: "application/json"},
			wantErr: ErrUnexpectedStatus,
		},
		{
			name:    "500",
			resp:    &Response{StatusCode: 500, ContentType: "application/json"},
			wantErr: ErrUnexpectedStatus,
		},
		{
			name:    "302 is not success",
			resp:    &Response{StatusCode: 302, ContentType: "application/json"},
			wantErr: ErrUnexpectedStatus,
		},
		{
			// The admin pointed at a login page rather than an API.
			name:    "html",
			resp:    &Response{StatusCode: 200, ContentType: "text/html"},
			wantErr: ErrUnexpectedType,
		},
		{
			name:    "malformed content type",
			resp:    &Response{StatusCode: 200, ContentType: "application/"},
			wantErr: ErrUnexpectedType,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckJSON(tc.resp)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckJSON() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckJSON() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestLimitDefaults(t *testing.T) {
	if got := (Options{}).timeout(); got != DefaultTimeout {
		t.Errorf("timeout() = %v, want %v", got, DefaultTimeout)
	}
	if got := (Options{}).maxBytes(); got != DefaultMaxResponseBytes {
		t.Errorf("maxBytes() = %v, want %v", got, DefaultMaxResponseBytes)
	}
	if got := (Options{Timeout: 1}).timeout(); got != 1 {
		t.Errorf("timeout() = %v, want the configured value", got)
	}
	if got := (Options{MaxResponseBytes: 42}).maxBytes(); got != 42 {
		t.Errorf("maxBytes() = %v, want the configured value", got)
	}
}

// fakeConn is the minimum net.Conn a race test needs: raceDial only ever
// stores it, returns it, or closes it.
type fakeConn struct {
	net.Conn
	addr   string
	closed atomic.Bool
}

func (c *fakeConn) Close() error { c.closed.Store(true); return nil }

func addrsOf(ips ...string) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
	}
	return out
}

// The bug this replaced: a first address that never answers cost the full
// DialTimeout before the good address was tried at all.
func TestRaceDialSkipsPastAnUnroutableAddress(t *testing.T) {
	f := &Fetcher{}
	f.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "[") {
			// The unroutable one: hangs until the context is cancelled.
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &fakeConn{addr: addr}, nil
	}

	started := time.Now()
	conn, err := f.raceDial(context.Background(), "tcp", addrsOf("2001:db8::1", "192.0.2.7"), "443")
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("raceDial() error = %v", err)
	}
	if got := conn.(*fakeConn).addr; got != "192.0.2.7:443" {
		t.Errorf("connected to %q, want the routable address", got)
	}
	// The good address starts one DialStagger in. Anything near DialTimeout
	// means the dials ran serially again.
	if elapsed > DialTimeout/2 {
		t.Errorf("took %s, want roughly %s — dials are not racing", elapsed, DialStagger)
	}
}

func TestRaceDialClosesLateConnections(t *testing.T) {
	// Atomic because the dial goroutine writes it while the test polls it.
	var late atomic.Pointer[fakeConn]
	f := &Fetcher{}
	f.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "[") {
			// Connects, but only after the second address has already won.
			time.Sleep(2 * DialStagger)
			c := &fakeConn{addr: addr}
			late.Store(c)
			return c, nil
		}
		return &fakeConn{addr: addr}, nil
	}

	conn, err := f.raceDial(context.Background(), "tcp", addrsOf("2001:db8::1", "192.0.2.7"), "443")
	if err != nil {
		t.Fatalf("raceDial() error = %v", err)
	}
	if conn.(*fakeConn).closed.Load() {
		t.Error("the winning connection was closed")
	}

	// drainConns runs in its own goroutine, so give it a moment to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c := late.Load(); c != nil && c.closed.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the late connection was leaked instead of closed")
}

func TestRaceDialAllAddressesFail(t *testing.T) {
	wantErr := errors.New("connection refused")
	f := &Fetcher{}
	f.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, wantErr
	}

	_, err := f.raceDial(context.Background(), "tcp", addrsOf("192.0.2.7", "192.0.2.8"), "443")
	if !errors.Is(err, wantErr) {
		t.Fatalf("raceDial() error = %v, want %v", err, wantErr)
	}
}

func TestRaceDialSingleAddress(t *testing.T) {
	f := &Fetcher{}
	f.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return &fakeConn{addr: addr}, nil
	}

	conn, err := f.raceDial(context.Background(), "tcp", addrsOf("192.0.2.7"), "443")
	if err != nil {
		t.Fatalf("raceDial() error = %v", err)
	}
	if got := conn.(*fakeConn).addr; got != "192.0.2.7:443" {
		t.Errorf("connected to %q", got)
	}
}

package fetcher

import (
	"context"
	"errors"
	"net/http"
	"testing"

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

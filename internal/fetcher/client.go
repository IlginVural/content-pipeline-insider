package fetcher

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"content-pipeline-insider/internal/security"
)

type Fetcher struct {
	client *http.Client
	opts   Options
}

func New(opts Options) *Fetcher {
	f := &Fetcher{opts: opts}

	transport := &http.Transport{
		DialContext:           f.guardedDial,
		TLSHandshakeTimeout:   TLSHandshakeTimeout,
		IdleConnTimeout:       IdleConnTimeout,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		ExpectContinueTimeout: 1 * time.Second,

		DisableCompression: false,

		// TLS 1.2, not 1.3. Requiring 1.3 refuses a large share of
		// partner APIs that are perfectly well configured but have not
		// moved yet, and it fails as an opaque handshake error that
		// reads like a network fault rather than a policy choice. 1.2
		// with modern cipher suites is the current baseline.
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	f.client = &http.Client{
		Timeout:       f.opts.timeout(),
		Transport:     transport,
		CheckRedirect: f.checkRedirect,
	}

	return f
}

func (f *Fetcher) guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", security.ErrBlockedTarget, addr)
	}

	// The addresses returned here are the ones dialed below. Re-resolving
	// before dialing would reopen the DNS rebinding window this check
	// exists to close: the name could answer differently the second time.
	addrs, err := security.ValidateHost(ctx, security.DefaultResolver, host, f.opts.policy())
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: DialTimeout}

	var lastErr error
	for _, a := range addrs {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no addresses for %s", host)
	}
	return nil, fmt.Errorf("%w: %s: %v", ErrDialFailed, host, lastErr)
}

// checkRedirect judges every hop with the same full check as the original
// URL — scheme, port, literal address, and DNS resolution.
//
// It previously checked only the scheme, port and literal IP, leaving the
// resolution check to guardedDial. That happened to hold, but only
// incidentally: a redirect to a public hostname resolving to loopback was
// stopped by the dialer rather than by the redirect guard, and surfaced
// as a connection failure instead of a blocked target.
func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	return security.ValidateRedirect(req.Context(), security.DefaultResolver, req, via, f.opts.policy())
}

// sends a request built by upstream and returns the response body
func (f *Fetcher) Do(ctx context.Context, req *http.Request) (*Response, error) {
	if err := security.ValidateURL(req.URL, f.opts.policy()); err != nil {
		return nil, err
	}

	resp, err := f.client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRequestFailed, err)
	}
	defer resp.Body.Close()

	max := f.opts.maxBytes()

	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRequestFailed, err)
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%w: larger than %d bytes", ErrResponseTooLarge, max)
	}

	return &Response{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
	}, nil
}

type Response struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

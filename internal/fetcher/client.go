package fetcher

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"content-pipeline-insider/internal/security"
)

type dialResult struct {
	conn net.Conn
	err  error
}

// dialFunc is net.Dialer.DialContext's signature. Injectable so the address
// race can be tested without real sockets.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type Fetcher struct {
	client *http.Client
	opts   Options
	dial   dialFunc
}

func New(opts Options) *Fetcher {
	f := &Fetcher{opts: opts}
	f.dial = (&net.Dialer{Timeout: DialTimeout}).DialContext

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

	conn, err := f.raceDial(ctx, network, addrs, port)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDialFailed, host, err)
	}
	return conn, nil
}

// raceDial starts a dial to every address, each staggered by DialStagger, and
// returns the first connection that succeeds. Losing dials are cancelled, and
// any connection that arrives after a winner is closed rather than leaked.
//
// Dialing serially instead — one address, wait for its timeout, then the next
// — meant a single unroutable address ahead of a good one cost DialTimeout on
// every request. That is the common case, not an edge case: a dual-stack host
// resolving to IPv6 first on a machine with no IPv6 route.
func (f *Fetcher) raceDial(ctx context.Context, network string, addrs []net.IPAddr, port string) (net.Conn, error) {
	if len(addrs) == 0 {
		return nil, errors.New("no addresses to dial")
	}

	// Not deferred: cancelling on the way out would kill the winning
	// connection. Losers are cancelled explicitly on each exit path.
	ctx, cancel := context.WithCancel(ctx)

	// Buffered by len(addrs) so a losing goroutine never blocks on send once
	// the winner has returned and stopped reading.
	results := make(chan dialResult, len(addrs))

	for i, a := range addrs {
		go func(i int, addr string) {
			if delay := time.Duration(i) * DialStagger; delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					results <- dialResult{err: ctx.Err()}
					return
				}
			}
			conn, err := f.dial(ctx, network, addr)
			results <- dialResult{conn: conn, err: err}
		}(i, net.JoinHostPort(a.IP.String(), port))
	}

	var lastErr error
	for i := 0; i < len(addrs); i++ {
		r := <-results
		if r.err == nil {
			cancel()
			// A slower address can still connect after the winner. Close
			// those rather than leak the socket.
			go drainConns(results, len(addrs)-i-1)
			return r.conn, nil
		}
		lastErr = r.err
	}

	cancel()
	if lastErr == nil {
		lastErr = errors.New("no address could be dialed")
	}
	return nil, lastErr
}

func drainConns(results <-chan dialResult, n int) {
	for i := 0; i < n; i++ {
		if r := <-results; r.conn != nil {
			r.conn.Close()
		}
	}
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

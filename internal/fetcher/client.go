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

		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
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

	addrs, err := security.ValidateHost(ctx, security.DefaultResolver, host)
	if err != nil {
		if !f.opts.allowLoopback {
			return nil, err
		}

		addrs, err = net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrBlockedTarget, host, err)
		}
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

func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= security.MaxRedirects {
		return fmt.Errorf("%w: more than %d", ErrTooManyRedirects, security.MaxRedirects)
	}
	if err := security.ValidateURL(req.URL, f.opts.AllowHTTP || f.opts.allowLoopback); err != nil {
		return err
	}
	return nil
}

// sends a request built by upstream and returns the response body
func (f *Fetcher) Do(ctx context.Context, req *http.Request) (*Response, error) {
	if err := security.ValidateURL(req.URL, f.opts.AllowHTTP || f.opts.allowLoopback); err != nil {
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

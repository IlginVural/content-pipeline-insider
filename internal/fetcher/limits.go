package fetcher

import "time"

const (
	DefaultTimeout = 5 * time.Second

	DefaultMaxResponseBytes int64 = 1 << 20

	DialTimeout = 3 * time.Second

	TLSHandshakeTimeout = 3 * time.Second

	IdleConnTimeout = 90 * time.Second
)

type Options struct {
	Timeout          time.Duration
	MaxResponseBytes int64

	AllowHTTP bool

	allowLoopback bool
}

func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultTimeout
}

func (o Options) maxBytes() int64 {
	if o.MaxResponseBytes > 0 {
		return o.MaxResponseBytes
	}
	return DefaultMaxResponseBytes
}

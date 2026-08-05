package fetcher

import (
	"time"

	"content-pipeline-insider/internal/security"
)

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

	// AllowHTTP permits plaintext http:// targets.
	AllowHTTP bool

	// AllowLoopback permits 127.0.0.0/8 and ::1 as targets, and nothing
	// else — every other reserved range, including the link-local block
	// holding the cloud metadata endpoint, stays refused. For pointing a
	// local test at a local server. Never set in production.
	AllowLoopback bool
}

// policy converts the fetch options into the address policy the security
// package enforces. Both exceptions are widenings of a single rule each;
// there is no combination of options that disables address checking.
func (o Options) policy() security.Policy {
	return security.Policy{
		AllowHTTP:     o.AllowHTTP,
		AllowLoopback: o.AllowLoopback,
	}
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

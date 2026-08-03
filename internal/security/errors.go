package security

import "errors"

var (
	ErrBlockedTarget    = errors.New("security: target address is not allowed")
	ErrBlockedScheme    = errors.New("security: URL scheme is not allowed")
	ErrBlockedPort      = errors.New("security: port is not allowed")
	ErrDNSFailure       = errors.New("security: could not resolve host")
	ErrTooManyRedirects = errors.New("security: too many redirects")
)

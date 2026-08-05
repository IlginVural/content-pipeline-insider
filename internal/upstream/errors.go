package upstream

import "errors"

var (
	ErrMissingParameter  = errors.New("upstream: required parameter is missing")
	ErrUnknownParameter  = errors.New("upstream: parameter is not declared in this config")
	ErrInvalidParameter  = errors.New("upstream: parameter value is invalid")
	ErrInvalidURL        = errors.New("upstream: could not build a valid URL")
	ErrInvalidHeader     = errors.New("upstream: invalid header configuration")
	ErrSecretResolution  = errors.New("upstream: failed to resolve secret-backed header")
	ErrUnsupportedMethod = errors.New("upstream: HTTP method is not allowed")
	ErrInvalidAuth       = errors.New("upstream: invalid authentication configuration")
	ErrInvalidBody       = errors.New("upstream: invalid request body template")
)

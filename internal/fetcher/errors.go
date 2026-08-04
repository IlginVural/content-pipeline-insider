package fetcher

import "errors"

var (
	ErrBlockedTarget    = errors.New("fetcher: target address is not allowed")
	ErrDialFailed       = errors.New("fetcher: could not connect")
	ErrRequestFailed    = errors.New("fetcher: request failed")
	ErrResponseTooLarge = errors.New("fetcher: response exceeded the size limit")
	ErrTooManyRedirects = errors.New("fetcher: too many redirects")
	ErrUnexpectedStatus = errors.New("fetcher: unexpected status code")
	ErrUnexpectedType   = errors.New("fetcher: unexpected content type")
)

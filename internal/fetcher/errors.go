package fetcher

import "errors"

// Address and redirect rejections are reported with the security
// package's sentinels rather than copies defined here. Two sentinels for
// one condition means errors.Is answers differently depending on which
// package the caller happened to import, and guardedDial previously
// returned one of each depending on the path taken.
var (
	ErrDialFailed       = errors.New("fetcher: could not connect")
	ErrRequestFailed    = errors.New("fetcher: request failed")
	ErrResponseTooLarge = errors.New("fetcher: response exceeded the size limit")
	ErrUnexpectedStatus = errors.New("fetcher: unexpected status code")
	ErrUnexpectedType   = errors.New("fetcher: unexpected content type")
)

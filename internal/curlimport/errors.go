package curlimport

import "errors"

var (
	ErrEmptyCommand      = errors.New("curlimport: empty command")
	ErrNotCurl           = errors.New(`curlimport: command must start with the literal token "curl"`)
	ErrUnterminatedQuote = errors.New("curlimport: unterminated quote")
	ErrTrailingBackslash = errors.New("curlimport: trailing backslash with no character to escape")
	ErrDangerousToken    = errors.New("curlimport: command contains a disallowed shell construct")
	ErrDangerousFlag     = errors.New("curlimport: flag is not allowed")
	ErrUnsupportedFlag   = errors.New("curlimport: flag is not supported in this MVP")
	ErrMissingFlagValue  = errors.New("curlimport: flag is missing its value")
	ErrMissingURL        = errors.New("curlimport: no URL found in command")
	ErrInvalidURL        = errors.New("curlimport: could not parse URL")
	ErrInvalidHeader     = errors.New(`curlimport: header must be in "Name: Value" form`)
	ErrInvalidUser       = errors.New(`curlimport: --user must be in "user:password" form`)
)
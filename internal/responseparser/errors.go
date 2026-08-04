package responseparser

import "errors"

var (
	ErrEmptyBody   = errors.New("responseparser: response body is empty")
	ErrInvalidJSON = errors.New("responseparser: response is not valid JSON")
	ErrTooDeep     = errors.New("responseparser: response nesting is too deep")
)

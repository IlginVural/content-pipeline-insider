package draft

import "errors"

var (
	ErrSecretStorage        = errors.New("draft: failed to store a secret")
	ErrUnsupportedBody      = errors.New("draft: request body cannot be represented")
	ErrUnsupportedMethod    = errors.New("draft: HTTP method is not allowed")
	ErrInvalidPath          = errors.New("draft: URL template is not parseable")
	ErrPathSegmentRange     = errors.New("draft: path segment index is out of range")
	ErrInvalidParameterName = errors.New("draft: parameter name is invalid")
	ErrDuplicateParameter   = errors.New("draft: parameter is already declared")
)

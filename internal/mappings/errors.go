package mappings

import "errors"

// Selection-time errors: the administrator's choices are malformed.
var (
	ErrEmptySet            = errors.New("mappings: no fields selected")
	ErrInvalidOutputName   = errors.New("mappings: output name must be a bare identifier")
	ErrOutputNameTooLong   = errors.New("mappings: output name is too long")
	ErrDuplicateOutputName = errors.New("mappings: two fields claim the same output name")
	ErrEmptySourcePath     = errors.New("mappings: source path is empty")
	ErrInvalidDataType     = errors.New("mappings: data type must be string, integer, number, or boolean")
	ErrDisplayLabelTooLong = errors.New("mappings: display label is too long")
	ErrNegativeSortOrder   = errors.New("mappings: sort order cannot be negative")
	ErrRequiredWithDefault = errors.New("mappings: a field cannot be both required and have a default")
	ErrBadDefault          = errors.New("mappings: default value does not match the declared type")
)

// Schema-validation errors: the choices do not match the response they were
// supposedly picked from.
var (
	ErrUnknownSourcePath = errors.New("mappings: source path is not a selectable field in the discovered schema")
	ErrTypeMismatch      = errors.New("mappings: declared type does not match the discovered field")
	ErrUntypedField      = errors.New("mappings: field was null in every sample, so its type is unknown")
)

// Runtime errors: the live response does not match what was stored.
var (
	ErrRequiredFieldMissing = errors.New("mappings: required field resolved to nothing")
	ErrWrongRuntimeType     = errors.New("mappings: partner value does not match the declared type")
)

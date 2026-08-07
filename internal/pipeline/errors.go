package pipeline

import "errors"

// Configuration errors: the caller handed us something the schema would
// reject anyway, caught early so the message names the field.
var (
	ErrInvalidStatus        = errors.New("pipeline: status is not one of draft, published, or archived")
	ErrInvalidVersionNumber = errors.New("pipeline: version number must start at 1")
	ErrIncompleteUpstream   = errors.New("pipeline: upstream config needs at least a method and a URL template")
	ErrEmptyTenant          = errors.New("pipeline: tenant id is required")
	ErrEmptyName            = errors.New("pipeline: name is required")
	ErrEmptyCreatedBy       = errors.New("pipeline: created-by user id is required")
)

// Lookup errors.
var (
	ErrPipelineNotFound = errors.New("pipeline: no such pipeline")
	ErrVersionNotFound  = errors.New("pipeline: no such version")
)

// Storage conflicts: the write was well-formed but lost a race or duplicated
// something the schema keeps unique.
var (
	// ErrDuplicateName is the tenant-scoped unique index on lower(name).
	ErrDuplicateName = errors.New("pipeline: a pipeline with this name already exists for the tenant")

	// ErrVersionConflict is UNIQUE (pipeline_id, version_number) rejecting a
	// second writer that allocated the same next number concurrently.
	ErrVersionConflict = errors.New("pipeline: that version number was taken concurrently, retry")
)

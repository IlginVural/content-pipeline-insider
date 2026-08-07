// Package pipeline persists the recipe the earlier stages produce: which
// partner API to call, which fields to take from its response, and under whose
// tenant and version that combination lives.
//
// The shapes here are the ones the other packages already defined —
// upstream.UpstreamConfig and mappings.FieldMapping — assembled into the rows
// the schema stores. Nothing is redescribed, because a second description of
// the same configuration is a second thing to keep in step.
package pipeline

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"content-pipeline-insider/internal/mappings"
	"content-pipeline-insider/internal/upstream"
)

// Status is a pipeline's lifecycle state, mirroring
// content_pipelines_status_check.
type Status string

const (
	// StatusDraft is an integration being configured. It has no active
	// version, so the runtime will not resolve it.
	StatusDraft Status = "draft"

	// StatusActive has a published version the runtime resolves.
	StatusActive Status = "active"

	// StatusPaused keeps the configuration but stops the runtime from using
	// it. Distinct from archived: pausing is expected to be undone.
	StatusPaused Status = "paused"

	StatusArchived Status = "archived"
)

func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusActive, StatusPaused, StatusArchived:
		return true
	}
	return false
}

// VersionStatus mirrors content_pipeline_versions_status_check.
//
// It is a different vocabulary from Status on purpose: a pipeline is paused,
// a version is published. Sharing one type would let a version be marked
// "paused", which the constraint rejects and which means nothing.
type VersionStatus string

const (
	VersionDraft     VersionStatus = "draft"
	VersionPublished VersionStatus = "published"
	VersionArchived  VersionStatus = "archived"
)

func (s VersionStatus) Valid() bool {
	switch s {
	case VersionDraft, VersionPublished, VersionArchived:
		return true
	}
	return false
}

// Pipeline is the stable identity of an integration. It outlives every version
// of its configuration, which is why campaigns bind to it rather than to a
// version.
type Pipeline struct {
	ID       uuid.UUID `json:"id"`
	TenantID uuid.UUID `json:"tenantId"`
	Name     string    `json:"name"`
	Status   Status    `json:"status"`

	// ActiveVersionID is the version the runtime resolves, uuid.Nil until one
	// is published. Nil rather than a pointer or a NullUUID: no real row can
	// ever have the nil UUID, so it cannot be mistaken for a live value, and
	// callers get one thing to compare against instead of two.
	ActiveVersionID uuid.UUID `json:"activeVersionId,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Version is one draft or published configuration: the whole recipe, in the
// two shapes the schema keeps it in.
type Version struct {
	ID            uuid.UUID `json:"id"`
	PipelineID    uuid.UUID `json:"pipelineId"`
	VersionNumber int       `json:"versionNumber"`

	// Upstream is stage 1, stored whole in the upstream_config JSONB column
	// because it is read whole and never queried by part.
	//
	// jsonb parses and re-serializes, so what comes back is semantically
	// identical rather than byte-identical: whitespace is normalized, object
	// keys come back sorted, and duplicate keys are dropped. This reaches
	// BodyTemplate, whose json.RawMessage is the one field here that holds
	// raw bytes. It is harmless — substituteBody decodes and re-marshals the
	// template before it is sent — but it does mean a stored config must
	// never be compared to its original with bytes.Equal.
	Upstream upstream.UpstreamConfig `json:"upstream"`

	// Mappings is stage 2, stored as one row per field. Always the complete
	// set for this version — the schema's (version_id, output_name) primary
	// key assumes it, and SaveDraftVersion writes them in one transaction so
	// a half-written selection cannot exist.
	Mappings []mappings.FieldMapping `json:"mappings"`

	Status    VersionStatus `json:"status"`
	CreatedBy uuid.UUID     `json:"createdBy"`
	CreatedAt time.Time     `json:"createdAt"`

	// PublishedAt is nil while the version is a draft. A pointer is right
	// here, unlike ActiveVersionID above: the zero time.Time is a real
	// timestamp, so it cannot double as "never published".
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

// Validate checks a version before it reaches the database, so a bad draft
// fails with a message naming the problem rather than as a constraint
// violation naming a constraint.
//
// It deliberately does not re-check what mappings.ValidateSet already covers;
// it calls it instead.
func (v Version) Validate() error {
	if !v.Status.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidStatus, v.Status)
	}
	if v.VersionNumber < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidVersionNumber, v.VersionNumber)
	}
	if v.PipelineID == uuid.Nil {
		return ErrPipelineNotFound
	}
	if v.CreatedBy == uuid.Nil {
		return ErrEmptyCreatedBy
	}

	// Stricter than the schema, which only requires upstream_config to be
	// present: a version with no method or URL cannot fetch anything, so
	// saving one produces a draft guaranteed to fail the moment it is tested.
	if v.Upstream.Method == "" || v.Upstream.URLTemplate == "" {
		return ErrIncompleteUpstream
	}

	return mappings.ValidateSet(v.Mappings)
}

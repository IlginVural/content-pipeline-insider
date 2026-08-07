package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"content-pipeline-insider/internal/mappings"
	"content-pipeline-insider/internal/upstream"
)

// Repository is every read and write of pipeline configuration.
//
// It borrows the pool rather than opening one: store.New already tunes and
// owns a single pgxpool for the service, and a second pool would double the
// connection budget for no benefit.
type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// CreatePipeline registers a new integration. It starts as a draft with no
// active version, because the configuration that would make it resolvable does
// not exist yet.
func (r *Repository) CreatePipeline(ctx context.Context, tenantID uuid.UUID, name string) (*Pipeline, error) {
	if tenantID == uuid.Nil {
		return nil, ErrEmptyTenant
	}
	if strings.TrimSpace(name) == "" {
		return nil, ErrEmptyName
	}

	const q = `
		INSERT INTO content_pipelines (tenant_id, name, status)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at
	`

	p := &Pipeline{TenantID: tenantID, Name: name, Status: StatusDraft}
	err := r.pool.QueryRow(ctx, q, tenantID, name, string(StatusDraft)).
		Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			// content_pipelines_tenant_name_idx is on lower(name), so this
			// fires for "Product API" against an existing "product api".
			return nil, fmt.Errorf("%w: %q", ErrDuplicateName, name)
		}
		return nil, fmt.Errorf("pipeline: create pipeline: %w", err)
	}
	return p, nil
}

// SaveDraftVersion writes a complete recipe as the pipeline's next draft
// version: the upstream config as one JSONB document, and the field selection
// as one row per field.
//
// Everything happens in one transaction. A version whose mapping rows failed
// halfway would be a configuration that looks saved and renders wrong, which
// is worse than a failed save.
func (r *Repository) SaveDraftVersion(
	ctx context.Context,
	pipelineID, createdBy uuid.UUID,
	cfg upstream.UpstreamConfig,
	set []mappings.FieldMapping,
) (*Version, error) {
	// Marshalling before opening the transaction keeps a malformed config
	// from holding a connection while it fails.
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("pipeline: marshal upstream config: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pipeline: begin: %w", err)
	}
	// Rollback after a successful commit is a no-op, so this is safe
	// unconditionally and covers every early return below.
	defer tx.Rollback(ctx)

	// Allocating the number in its own statement, rather than inside the
	// INSERT, is what lets the version be fully validated before anything is
	// written. GROUP BY is load-bearing: without it the aggregate returns a
	// row even when the pipeline does not exist, and a missing pipeline would
	// silently become version 1.
	const allocate = `
		SELECT COALESCE(MAX(v.version_number), 0) + 1
		FROM content_pipelines p
		LEFT JOIN content_pipeline_versions v ON v.pipeline_id = p.id
		WHERE p.id = $1
		GROUP BY p.id
	`

	var number int
	if err := tx.QueryRow(ctx, allocate, pipelineID).Scan(&number); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrPipelineNotFound, pipelineID)
		}
		return nil, fmt.Errorf("pipeline: allocate version number: %w", err)
	}

	version := &Version{
		PipelineID:    pipelineID,
		VersionNumber: number,
		Upstream:      cfg,
		Mappings:      set,
		Status:        VersionDraft,
		CreatedBy:     createdBy,
	}
	if err := version.Validate(); err != nil {
		return nil, err
	}

	const insertVersion = `
		INSERT INTO content_pipeline_versions
			(pipeline_id, version_number, upstream_config, status, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`

	err = tx.QueryRow(ctx, insertVersion,
		pipelineID, number, configJSON, string(VersionDraft), createdBy,
	).Scan(&version.ID, &version.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			// Two callers allocated the same number before either committed.
			// UNIQUE (pipeline_id, version_number) is what makes the read
			// above safe to do without locking the pipeline row.
			return nil, fmt.Errorf("%w: version %d of %s", ErrVersionConflict, number, pipelineID)
		}
		return nil, fmt.Errorf("pipeline: insert version: %w", err)
	}

	if err := insertMappings(ctx, tx, version.ID, set); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pipeline: commit: %w", err)
	}
	return version, nil
}

// insertMappings writes the whole selection as one batch: one round trip
// instead of one per field, which matters because a realistic selection is
// tens of fields.
func insertMappings(ctx context.Context, tx pgx.Tx, versionID uuid.UUID, set []mappings.FieldMapping) error {
	const q = `
		INSERT INTO content_pipeline_field_mappings
			(version_id, output_name, source_path, data_type,
			 display_label, required, default_value, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	batch := &pgx.Batch{}
	for _, m := range set {
		// Both columns are nullable, and NULL is the honest encoding of
		// "unset". An empty string would be a second way to say the same
		// thing, and field_mappings_default_type_check rejects a JSON null
		// outright.
		var label, def any
		if m.DisplayLabel != "" {
			label = m.DisplayLabel
		}
		if m.DefaultValue != nil {
			def = []byte(m.DefaultValue)
		}

		batch.Queue(q, versionID, m.OutputName, m.SourcePath, string(m.DataType),
			label, m.Required, def, m.SortOrder)
	}

	results := tx.SendBatch(ctx, batch)
	for i := range set {
		if _, err := results.Exec(); err != nil {
			results.Close()
			// Naming the field turns "check constraint violated" into
			// something an administrator can act on.
			return fmt.Errorf("pipeline: insert mapping %q: %w", set[i].OutputName, err)
		}
	}
	// The batch must be closed before the transaction is used again: Commit
	// on a connection with unread batch results fails.
	if err := results.Close(); err != nil {
		return fmt.Errorf("pipeline: close mapping batch: %w", err)
	}
	return nil
}

// GetVersion loads one version and its complete field selection.
//
// Two queries rather than a join: a join would repeat the whole upstream
// config once per mapping row, and the mappings are a set that is only ever
// written atomically, so reading them separately cannot observe a partial one.
func (r *Repository) GetVersion(ctx context.Context, pipelineID uuid.UUID, versionNumber int) (*Version, error) {
	const q = `
		SELECT id, upstream_config, status, created_by, created_at, published_at
		FROM content_pipeline_versions
		WHERE pipeline_id = $1 AND version_number = $2
	`

	version := &Version{PipelineID: pipelineID, VersionNumber: versionNumber}

	var configJSON []byte
	var status string
	var publishedAt *time.Time

	err := r.pool.QueryRow(ctx, q, pipelineID, versionNumber).
		Scan(&version.ID, &configJSON, &status, &version.CreatedBy, &version.CreatedAt, &publishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: version %d of %s", ErrVersionNotFound, versionNumber, pipelineID)
		}
		return nil, fmt.Errorf("pipeline: get version: %w", err)
	}

	if err := json.Unmarshal(configJSON, &version.Upstream); err != nil {
		return nil, fmt.Errorf("pipeline: decode upstream config: %w", err)
	}
	version.Status = VersionStatus(status)
	version.PublishedAt = publishedAt

	version.Mappings, err = r.fieldMappings(ctx, version.ID)
	if err != nil {
		return nil, err
	}
	return version, nil
}

// PipelineSummary is a pipeline plus the counts a listing needs, so a caller
// rendering an index does not fan out one query per row.
type PipelineSummary struct {
	Pipeline
	VersionCount  int `json:"versionCount"`
	LatestVersion int `json:"latestVersion"`
}

// ListRecentPipelines returns the newest pipelines across ALL tenants.
//
// It is deliberately not tenant-scoped, which makes it an operator and local
// development view rather than a customer-facing one: a multi-tenant API must
// never expose a cross-tenant list. Callers are responsible for keeping it out
// of production — internal/api gates it on the environment.
func (r *Repository) ListRecentPipelines(ctx context.Context, limit int) ([]PipelineSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
		-- active_version_id is NULL until a version is published, and
		-- Pipeline.ActiveVersionID is a plain uuid.UUID whose documented
		-- "none" value is uuid.Nil. Coalescing keeps the two agreeing
		-- instead of failing the scan.
		SELECT p.id, p.tenant_id, p.name, p.status,
		       COALESCE(p.active_version_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       p.created_at, p.updated_at,
		       COUNT(v.id), COALESCE(MAX(v.version_number), 0)
		FROM content_pipelines p
		LEFT JOIN content_pipeline_versions v ON v.pipeline_id = p.id
		GROUP BY p.id
		ORDER BY p.created_at DESC
		LIMIT $1
	`

	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("pipeline: list pipelines: %w", err)
	}
	defer rows.Close()

	var out []PipelineSummary
	for rows.Next() {
		var s PipelineSummary
		var status string
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Name, &status, &s.ActiveVersionID,
			&s.CreatedAt, &s.UpdatedAt, &s.VersionCount, &s.LatestVersion); err != nil {
			return nil, fmt.Errorf("pipeline: scan pipeline: %w", err)
		}
		s.Status = Status(status)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pipeline: list pipelines: %w", err)
	}
	return out, nil
}

// VersionSummary describes a version without loading its config or mappings.
type VersionSummary struct {
	VersionNumber int           `json:"versionNumber"`
	Status        VersionStatus `json:"status"`
	FieldCount    int           `json:"fieldCount"`
	CreatedAt     time.Time     `json:"createdAt"`
	PublishedAt   *time.Time    `json:"publishedAt,omitempty"`
}

func (r *Repository) ListVersions(ctx context.Context, pipelineID uuid.UUID) ([]VersionSummary, error) {
	const q = `
		SELECT v.version_number, v.status, COUNT(m.output_name),
		       v.created_at, v.published_at
		FROM content_pipeline_versions v
		LEFT JOIN content_pipeline_field_mappings m ON m.version_id = v.id
		WHERE v.pipeline_id = $1
		GROUP BY v.id
		ORDER BY v.version_number DESC
	`

	rows, err := r.pool.Query(ctx, q, pipelineID)
	if err != nil {
		return nil, fmt.Errorf("pipeline: list versions: %w", err)
	}
	defer rows.Close()

	var out []VersionSummary
	for rows.Next() {
		var v VersionSummary
		var status string
		if err := rows.Scan(&v.VersionNumber, &status, &v.FieldCount, &v.CreatedAt, &v.PublishedAt); err != nil {
			return nil, fmt.Errorf("pipeline: scan version: %w", err)
		}
		v.Status = VersionStatus(status)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pipeline: list versions: %w", err)
	}
	return out, nil
}

// fieldMappings reads a version's selection in display order. The ORDER BY
// matches field_mappings_version_order_idx, and its second term makes ties
// deterministic — two fields may legitimately share a sort_order.
func (r *Repository) fieldMappings(ctx context.Context, versionID uuid.UUID) ([]mappings.FieldMapping, error) {
	const q = `
		SELECT output_name, source_path, data_type,
		       display_label, required, default_value, sort_order
		FROM content_pipeline_field_mappings
		WHERE version_id = $1
		ORDER BY sort_order, output_name
	`

	rows, err := r.pool.Query(ctx, q, versionID)
	if err != nil {
		return nil, fmt.Errorf("pipeline: query field mappings: %w", err)
	}
	defer rows.Close()

	var out []mappings.FieldMapping
	for rows.Next() {
		var m mappings.FieldMapping
		var dataType string
		var label *string
		var def []byte

		if err := rows.Scan(&m.OutputName, &m.SourcePath, &dataType,
			&label, &m.Required, &def, &m.SortOrder); err != nil {
			return nil, fmt.Errorf("pipeline: scan field mapping: %w", err)
		}

		m.DataType = mappings.DataType(dataType)
		if label != nil {
			m.DisplayLabel = *label
		}
		if def != nil {
			m.DefaultValue = json.RawMessage(def)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pipeline: read field mappings: %w", err)
	}
	return out, nil
}

// isUniqueViolation reports whether err is Postgres 23505.
//
// Checking the SQLSTATE rather than the message is what keeps this working
// when the server's locale changes the wording.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

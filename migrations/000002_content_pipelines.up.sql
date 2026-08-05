-- The three pipeline stages live in three different shapes, on purpose.
--
--   Upstream       -> content_pipeline_versions.upstream_config (JSONB)
--   Transformation -> content_pipeline_field_mappings (one row per field)
--   Rendering      -> content_pipeline_versions.renderer_config (JSONB)
--
-- Upstream and renderer config are read whole and never queried by part, so
-- they stay documents. Transformation is the one stage with per-field
-- structure worth constraining, so it becomes rows.
--
-- Nothing here stores the discovered field tree. The tree exists so an
-- administrator can pick fields; once they have picked, the selected mappings
-- carry everything the runtime needs. Persisting both would store the same
-- information twice, and the copy nobody reads is free to drift away from the
-- partner API it described.

-- content_pipelines is the stable identity of an integration. It outlives
-- every version of its configuration.
CREATE TABLE IF NOT EXISTS content_pipelines (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID         NOT NULL,
    name              VARCHAR(255) NOT NULL,
    status            VARCHAR(32)  NOT NULL,

    -- Which version the runtime resolves. NULL until one is published. The
    -- foreign key is added at the bottom of this file, once the versions
    -- table it points at exists.
    active_version_id UUID,

    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT content_pipelines_status_check
        CHECK (status IN ('draft', 'active', 'paused', 'archived'))
);

-- Pipeline names are human-facing. Two integrations in one tenant differing
-- only in capitalisation are a support ticket waiting to happen.
CREATE UNIQUE INDEX IF NOT EXISTS content_pipelines_tenant_name_idx
    ON content_pipelines (tenant_id, lower(name));


-- content_pipeline_versions holds one draft or published configuration.
-- Published rows are immutable: a change means a new version, so a campaign
-- already sending cannot have the ground shift under it.
CREATE TABLE IF NOT EXISTS content_pipeline_versions (
    id             UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_id    UUID    NOT NULL
        REFERENCES content_pipelines (id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL,

    -- Stage 1. A marshalled upstream.UpstreamConfig: method, URL template,
    -- authentication, and parameter definitions.
    upstream_config  JSONB NOT NULL,

    -- Stage 3. Namespace and per-type formatting defaults. Nullable because
    -- the renderer does not exist yet, and a NOT NULL column here would only
    -- be satisfied by writing '{}' and calling it configured.
    renderer_config  JSONB,
    execution_config JSONB,
    fallback_config  JSONB,

    status       VARCHAR(32) NOT NULL,
    created_by   UUID        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,

    UNIQUE (pipeline_id, version_number),

    CONSTRAINT content_pipeline_versions_status_check
        CHECK (status IN ('draft', 'published', 'archived'))
);

-- There is deliberately no transformer_config column: the mappings table
-- below replaces it. There is no output_schema column either, because it is
-- fully derivable from those rows, and a stored copy that can drift from the
-- rows it describes is the same mistake as storing the tree.

ALTER TABLE content_pipelines
    ADD CONSTRAINT content_pipelines_active_version_fk
    FOREIGN KEY (active_version_id)
    REFERENCES content_pipeline_versions (id) ON DELETE SET NULL;


-- Stage 2. One row per field the administrator selected, and only those.
-- Unselected fields, sample values, and the full partner response are all
-- discarded rather than persisted.
CREATE TABLE IF NOT EXISTS content_pipeline_field_mappings (
    version_id    UUID         NOT NULL
        REFERENCES content_pipeline_versions (id) ON DELETE CASCADE,

    -- The name the email template writes: {{externalProduct.availableStock}}.
    output_name   VARCHAR(128) NOT NULL,

    -- The JMESPath the transformer evaluates against the live response. This
    -- is schemainfer.SchemaNode.JMESPath for the node the admin picked, so it
    -- can carry array projections ("reviews[].rating") and quoted keys
    -- ("order-id") rather than only dotted paths.
    source_path   TEXT         NOT NULL,

    -- Drives both coercion (is the value what the admin declared) and
    -- rendering (separators, decimals, escaping, conditional blocks).
    data_type     VARCHAR(16)  NOT NULL,

    -- What the marketer reads in the field picker. output_name is for code.
    display_label VARCHAR(255),

    required      BOOLEAN      NOT NULL DEFAULT FALSE,
    default_value JSONB,
    sort_order    INTEGER      NOT NULL,

    -- Rows are always written as a complete set for a version and are never
    -- referenced individually, so the natural key is the whole key. It also
    -- indexes the version-scoped lookup for free.
    PRIMARY KEY (version_id, output_name),

    -- output_name becomes a template variable. Anything that is not a bare
    -- identifier breaks at render time, far from where it was entered.
    CONSTRAINT field_mappings_output_name_check
        CHECK (output_name ~ '^[A-Za-z_][A-Za-z0-9_]*$'),

    CONSTRAINT field_mappings_source_path_check
        CHECK (length(btrim(source_path)) > 0),

    -- Only scalars are selectable in schemainfer, so only scalars arrive
    -- here. 'null' is excluded: a field observed only as null has no known
    -- type, and storing one would mean nothing downstream.
    CONSTRAINT field_mappings_data_type_check
        CHECK (data_type IN ('string', 'integer', 'number', 'boolean')),

    -- sort_order is a display position, so a negative is always a bug --
    -- an off-by-one in the client, or a signed value that was never set.
    CONSTRAINT field_mappings_sort_order_check
        CHECK (sort_order >= 0),

    -- A string field cannot default to false. This is the check a config blob
    -- cannot give you.
    --
    -- Note that a JSON null fails every branch, because jsonb_typeof returns
    -- 'null' for it. That is intended: a null default carries no value, so
    -- SQL NULL is the only way to say "no default".
    CONSTRAINT field_mappings_default_type_check
        CHECK (
            default_value IS NULL
            OR (data_type = 'string' AND jsonb_typeof(default_value) = 'string')
            OR (data_type IN ('integer', 'number') AND jsonb_typeof(default_value) = 'number')
            OR (data_type = 'boolean' AND jsonb_typeof(default_value) = 'boolean')
        ),

    -- required means "abort or fall back when this resolves to nothing".
    -- A default means it can never resolve to nothing. Asking for both is an
    -- administrator who has not decided which behaviour they want.
    CONSTRAINT field_mappings_required_default_check
        CHECK (NOT (required AND default_value IS NOT NULL))
);

-- The read pattern is "every field for this version, in display order", which
-- both the render path and the editor's field picker issue.
CREATE INDEX IF NOT EXISTS field_mappings_version_order_idx
    ON content_pipeline_field_mappings (version_id, sort_order);

-- The reverse question, which is impact analysis rather than rendering:
-- "which versions read product.pricing.current?" Asked when a partner renames
-- or drops a field and someone has to find every integration that breaks. A
-- JSONB config blob could only answer this with a full scan.
CREATE INDEX IF NOT EXISTS field_mappings_source_path_idx
    ON content_pipeline_field_mappings (source_path);
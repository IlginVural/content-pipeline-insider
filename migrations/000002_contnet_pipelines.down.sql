-- Reverse dependency order. The self-referencing FK from content_pipelines
-- back to content_pipeline_versions has to go before the versions table can
-- be dropped.
DROP INDEX IF EXISTS field_mappings_source_path_idx;
DROP INDEX IF EXISTS field_mappings_version_order_idx;
DROP TABLE IF EXISTS content_pipeline_field_mappings;

ALTER TABLE IF EXISTS content_pipelines
    DROP CONSTRAINT IF EXISTS content_pipelines_active_version_fk;

DROP TABLE IF EXISTS content_pipeline_versions;

DROP INDEX IF EXISTS content_pipelines_tenant_name_idx;
DROP TABLE IF EXISTS content_pipelines;
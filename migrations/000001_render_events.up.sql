-- render_events records one row per render attempt: which source was
-- resolved, whether the cache served it, and how long it took. It is the
-- execution-metadata table the README's storage section calls for.
--
-- source_id is TEXT rather than UUID because store.RenderEvent carries a
-- plain string. Tightening it to UUID is a change to the Go type first.
CREATE TABLE IF NOT EXISTS render_events (
    id          BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id   TEXT        NOT NULL,
    cached      BOOLEAN     NOT NULL,
    duration_ms INTEGER     NOT NULL,
    status      INTEGER     NOT NULL,
    rendered_at TIMESTAMPTZ NOT NULL
);

-- The obvious read pattern is "recent events, newest first", either
-- across all sources or scoped to one.
CREATE INDEX IF NOT EXISTS render_events_rendered_at_idx
    ON render_events (rendered_at DESC);

CREATE INDEX IF NOT EXISTS render_events_source_rendered_at_idx
    ON render_events (source_id, rendered_at DESC);

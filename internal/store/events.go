package store

import (
	"context"
	"fmt"
	"time"
)

type RenderEvent struct {
	SourceID   string
	Cached     bool
	DurationMS int
	Status     int
	RenderedAt time.Time
}


func (s *Store) InsertRenderEvent(ctx context.Context, e RenderEvent) error {
	const q = `
		INSERT INTO render_events (source_id, cached, duration_ms, status, rendered_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := s.pool.Exec(ctx, q, e.SourceID, e.Cached, e.DurationMS, e.Status, e.RenderedAt)
	if err != nil {
		return fmt.Errorf("store: insert render event: %w", err)
	}
	return nil
}

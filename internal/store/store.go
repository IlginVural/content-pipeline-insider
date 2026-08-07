// Package store owns the service's PostgreSQL connection pool and the tables
// that belong to no single domain package, such as the render_events audit
// trail.
//
// It is not the only package that issues SQL. A domain package that owns its
// own types — internal/pipeline, for one — owns the queries for them too, and
// borrows the pool from here through Pool. The alternative, funnelling every
// table through this package, would make every caller import store just to
// name a type.
//
// Design notes:
//   - We use pgx (not database/sql) because pgx is faster, has better
//     Postgres-specific features (LISTEN/NOTIFY, arrays, JSONB), and the
//     pgxpool.Pool is safe for concurrent use so we can share one instance
//     across the whole service.
//   - The Store type is a thin wrapper. Handlers never touch pgxpool
//     directly — they call methods on Store, or on a repository built from
//     Pool. That keeps one place to tune connection pool settings and to add
//     cross-cutting behavior (metrics, tracing) later.
//   - Every DB method takes a context.Context first. This is how requests
//     that have been cancelled (client disconnected, deadline hit) don't
//     keep churning through the pool.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the handle every subsystem uses to talk to Postgres.
type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse dsn: %w", err)
	}

	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Pool lends the connection pool to repositories that live in their own
// package. Store keeps ownership: it created the pool, tuned it, and Close
// still ends it, so a borrower must not close what it is handed.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Close() {
	s.pool.Close()
}

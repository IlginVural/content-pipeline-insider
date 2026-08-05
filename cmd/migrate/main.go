// Command migrate applies or rolls back the PostgreSQL schema.
//
// It is a separate binary from renderd on purpose. Running migrations on
// service startup means every replica races to mutate the schema during a
// rollout, and it forces the service's database role to hold DDL rights it
// otherwise never needs. Keeping it separate makes schema changes an
// explicit step — `make migrate` locally, one job in a deploy.
//
// Usage:
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate down    # rolls back exactly one migration
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"content-pipeline-insider/internal/config"
	"content-pipeline-insider/migrations"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate up|down")
		os.Exit(2)
	}
	command := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		fail("load config", err)
	}

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		fail("read embedded migrations", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, pgxDSN(cfg.DatabaseURL))
	if err != nil {
		fail("open migrator", err)
	}
	defer m.Close()

	switch command {
	case "up":
		err = m.Up()
	case "down":
		// Steps(-1), not Down(): Down() tears down the entire schema, which
		// is rarely what someone typing "down" wants and is unrecoverable.
		err = m.Steps(-1)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q: use up or down\n", command)
		os.Exit(2)
	}

	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		fail(command, err)
	}

	version, dirty, verr := m.Version()
	switch {
	// An empty schema is the expected state after rolling back the last
	// migration, not a failure — report it rather than exiting non-zero.
	case errors.Is(verr, migrate.ErrNilVersion):
		fmt.Println("no migrations applied")
	case verr != nil:
		fail("read schema version", verr)
	default:
		fmt.Printf("schema version %d (dirty=%t)\n", version, dirty)
	}
}

// pgxDSN rewrites the connection URL onto the scheme golang-migrate's
// pgx/v5 driver registers itself under. The alternative is migrate's
// default "postgres" driver, which would pull lib/pq into a module that
// has otherwise standardized on pgx.
func pgxDSN(dsn string) string {
	for _, scheme := range []string{"postgresql://", "postgres://"} {
		if strings.HasPrefix(dsn, scheme) {
			return "pgx5://" + strings.TrimPrefix(dsn, scheme)
		}
	}
	return dsn
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "migrate: %s: %v\n", what, err)
	os.Exit(1)
}

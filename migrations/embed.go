// Package migrations embeds the SQL schema files so the migration
// runner carries them inside the binary. Without this the runner would
// depend on the migrations directory existing next to it at runtime,
// which holds for `make migrate` in a checkout and fails for a deployed
// container.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

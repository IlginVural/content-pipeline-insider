package secrets

// Package secrets defines how the rest of the platform looks up a
// credential by reference, without knowing or caring where it's
// actually stored. internal/upstream's request builder depends only on
// this interface, never on a concrete secret store, so it can be
// built and tested today with MemoryResolver, and pointed at real AWS
// Secrets Manager later by adding one new file, not by changing any
// existing caller.

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("secrets: reference not found")

type Resolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

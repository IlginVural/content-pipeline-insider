// Package secrets defines how the platform stores and looks up
// credentials by reference, without callers knowing where they actually
// live. Consumers depend on the narrow interface they need — the
// request builder only reads, the cURL-import step only writes — so
// swapping the in-memory implementation for real AWS Secrets Manager
// means adding one file, not editing any caller.
package secrets

import (
	"context"
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("secrets: reference not found")

// Resolver reads a credential. internal/upstream depends on this alone.
type Resolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// Storer writes a credential and is used only by the cURL-import step,
// which lifts plaintext values out of the parsed command. Kept separate
// from Resolver so the request builder is structurally incapable of
// writing secrets.
type Storer interface {
	Store(ctx context.Context, ref, value string) error
}

// Reference builds the canonical secret name. Centralized so every
// caller produces the same layout
func Reference(tenantID, draftID, name string) string {
	return fmt.Sprintf("%s/content-pipelines/%s/%s", tenantID, draftID, name)
}

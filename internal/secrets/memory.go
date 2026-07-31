package secrets

import "context"

// MemoryResolver is an in-memory Resolver for local development and
// tests. It exists so internal/upstream and later the pipeline
// integration test can run without any real secret backend — construct
// it with the values a test needs and nothing else has to know it
// isn't AWS Secrets Manager.
type MemoryResolver struct {
	values map[string]string
}

// NewMemoryResolver builds a resolver from a fixed set of reference ->
// value pairs. The map is copied so callers can't mutate it out from
// under the resolver after construction.
func NewMemoryResolver(values map[string]string) *MemoryResolver {
	copied := make(map[string]string, len(values))
	for k, v := range values {
		copied[k] = v
	}
	return &MemoryResolver{values: copied}
}

func (m *MemoryResolver) Resolve(_ context.Context, ref string) (string, error) {
	v, ok := m.values[ref]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

package secrets

import (
	"context"
	"sync"
)

// MemoryResolver is an in-memory Resolver and Storer for local
// development and tests. It exists so internal/upstream and the draft
// conversion step can run without any real secret backend — construct
// it with the values a test needs and nothing else has to know it isn't
// AWS Secrets Manager.
type MemoryResolver struct {
	mu     sync.RWMutex
	values map[string]string
}

// Compile-time proof that MemoryResolver satisfies both interfaces, so
// a signature drift breaks the build here rather than at a call site.
var (
	_ Resolver = (*MemoryResolver)(nil)
	_ Storer   = (*MemoryResolver)(nil)
)

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
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.values[ref]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Store writes a credential. The lock matters now that values are
// written after construction: the draft step stores while other
// goroutines may be resolving, which is a data race without it.
func (m *MemoryResolver) Store(_ context.Context, ref, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.values == nil {
		m.values = make(map[string]string)
	}
	m.values[ref] = value
	return nil
}

// Package resolver runs a stored configuration against its partner API and
// returns the normalized object a template renders.
//
// It owns no logic of its own — it is the order the other packages run in.
// Both cmd/pipelinetry and the HTTP API go through here so the two cannot
// drift into rendering the same configuration differently.
package resolver

import (
	"context"
	"fmt"
	"time"

	"content-pipeline-insider/internal/fetcher"
	"content-pipeline-insider/internal/mappings"
	"content-pipeline-insider/internal/responseparser"
	"content-pipeline-insider/internal/schemainfer"
	"content-pipeline-insider/internal/secrets"
	"content-pipeline-insider/internal/upstream"
)

type Resolver struct {
	fetch   *fetcher.Fetcher
	secrets secrets.Resolver
}

func New(f *fetcher.Fetcher, s secrets.Resolver) *Resolver {
	return &Resolver{fetch: f, secrets: s}
}

// Result carries every stage's output, not just the final object, because the
// CLI prints each stage and the discovery flow needs Tree before any mapping
// exists.
type Result struct {
	Output  map[string]any
	Tree    *schemainfer.SchemaNode
	Fields  []schemainfer.SchemaNode
	Status  int
	Elapsed time.Duration
	Body    []byte
	Parsed  any
}

// Resolve fetches, parses, and transforms. A nil mapping set stops after
// discovery: Tree and Fields are populated and Output is nil, which is what
// the administrator sees before choosing fields.
func (r *Resolver) Resolve(
	ctx context.Context,
	cfg upstream.UpstreamConfig,
	set []mappings.FieldMapping,
	params map[string]string,
) (*Result, error) {
	req, err := upstream.BuildRequest(ctx, cfg, params, r.secrets)
	if err != nil {
		return nil, err
	}

	started := time.Now()
	resp, err := r.fetch.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(started)

	res := &Result{
		Status:  resp.StatusCode,
		Elapsed: elapsed,
		Body:    resp.Body,
	}

	if err := fetcher.CheckJSON(resp); err != nil {
		return res, err
	}

	parsed, err := responseparser.Decode(resp.Body)
	if err != nil {
		return res, err
	}
	res.Parsed = parsed

	// One sample. Infer takes a slice because several responses describe an
	// optional field better than one does.
	tree, err := schemainfer.Infer([]any{parsed})
	if err != nil {
		return res, err
	}
	res.Tree = tree
	res.Fields = schemainfer.Flatten(tree)

	if set == nil {
		return res, nil
	}

	if err := mappings.ValidateSet(set); err != nil {
		return res, err
	}
	// Run for stored sets too: it proves the selection still matches what the
	// partner returns today, rather than what it returned when it was saved.
	if err := mappings.ValidateAgainstSchema(tree, set); err != nil {
		return res, err
	}

	out, err := mappings.Apply(set, parsed)
	if err != nil {
		return res, err
	}
	res.Output = out
	return res, nil
}

// MissingSecrets returns the secret references a config needs that the
// resolver cannot supply. Callers use it to fail with "which credential is
// missing" instead of a bare resolution error.
func MissingSecrets(ctx context.Context, cfg upstream.UpstreamConfig, s secrets.Resolver) []string {
	var missing []string
	check := func(ref string) {
		if ref == "" {
			return
		}
		if _, err := s.Resolve(ctx, ref); err != nil {
			missing = append(missing, ref)
		}
	}
	for _, h := range cfg.Headers {
		check(h.SecretReference)
	}
	for _, q := range cfg.QueryParameters {
		check(q.SecretReference)
	}
	if cfg.Authentication != nil {
		check(cfg.Authentication.SecretReference)
	}
	return missing
}

// Describe renders a one-line summary for logs.
func Describe(res *Result) string {
	if res == nil {
		return "no result"
	}
	return fmt.Sprintf("status=%d fields=%d elapsed=%s", res.Status, len(res.Output), res.Elapsed)
}

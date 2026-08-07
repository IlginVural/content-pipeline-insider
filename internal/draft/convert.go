// Package draft converts a parsed cURL command into the persisted upstream
// configuration, moving every plaintext credential into the secret store on
// the way. What it returns is safe to persist: references, never values.
package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"content-pipeline-insider/internal/curlimport"
	"content-pipeline-insider/internal/secrets"
	"content-pipeline-insider/internal/upstream"
)

const DefaultTimeoutMs = 5000

// Secret names are namespaced by origin so a header and a query parameter
// sharing a name cannot overwrite each other's credential.
const (
	headerSecretPrefix = "header/"
	querySecretPrefix  = "query/"
	authSecretName     = "auth"
)

var supportedMethods = map[string]bool{
	http.MethodGet:  true,
	http.MethodPost: true,
}

// FromImported turns a parsed cURL command into an UpstreamConfig.
//
// Parameters is left empty: which path segment becomes dynamic is an
// administrator's decision, not something inferable from a URL. Call
// MakePathDynamic for each segment they pick.
func FromImported(
	ctx context.Context,
	req curlimport.ImportedRequest,
	tenantID, draftID string,
	store secrets.Storer,
) (upstream.UpstreamConfig, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !supportedMethods[method] {
		return upstream.UpstreamConfig{}, fmt.Errorf("%w: %s", ErrUnsupportedMethod, method)
	}

	cfg := upstream.UpstreamConfig{
		Method:      method,
		URLTemplate: req.BaseURL + req.Path,
		TimeoutMs:   DefaultTimeoutMs,
		Parameters:  map[string]upstream.ParameterDef{},
	}

	for _, h := range req.Headers {
		if !h.IsSensitive {
			cfg.Headers = append(cfg.Headers, upstream.Header{
				Name:   h.Name,
				Source: upstream.HeaderStatic,
				Value:  h.Value,
			})
			continue
		}
		ref := secrets.Reference(tenantID, draftID, headerSecretPrefix+h.Name)
		if err := store.Store(ctx, ref, h.Value); err != nil {
			return upstream.UpstreamConfig{}, fmt.Errorf("%w: header %s: %v", ErrSecretStorage, h.Name, err)
		}
		// The whole value moves, not just the token after "Bearer": Format
		// defaults to "{{secret}}", so it returns to the wire byte-for-byte.
		cfg.Headers = append(cfg.Headers, upstream.Header{
			Name:            h.Name,
			Source:          upstream.HeaderSecret,
			SecretReference: ref,
		})
	}

	for _, qp := range req.Query {
		if !qp.IsSensitive {
			cfg.QueryParameters = append(cfg.QueryParameters, upstream.QueryParam{
				Name:   qp.Name,
				Source: upstream.HeaderStatic,
				Value:  qp.Value,
			})
			continue
		}
		ref := secrets.Reference(tenantID, draftID, querySecretPrefix+qp.Name)
		if err := store.Store(ctx, ref, qp.Value); err != nil {
			return upstream.UpstreamConfig{}, fmt.Errorf("%w: query %s: %v", ErrSecretStorage, qp.Name, err)
		}
		cfg.QueryParameters = append(cfg.QueryParameters, upstream.QueryParam{
			Name:            qp.Name,
			Source:          upstream.HeaderSecret,
			SecretReference: ref,
		})
	}

	if req.Auth != nil {
		ref := secrets.Reference(tenantID, draftID, authSecretName)
		// "user:password" verbatim — the format applyAuthentication expects.
		if err := store.Store(ctx, ref, req.Auth.Username+":"+req.Auth.Password); err != nil {
			return upstream.UpstreamConfig{}, fmt.Errorf("%w: basic auth: %v", ErrSecretStorage, err)
		}
		cfg.Authentication = &upstream.Authentication{
			Type:            upstream.AuthBasic,
			SecretReference: ref,
		}
	}

	if body := strings.TrimSpace(req.Body); body != "" {
		// BodyTemplate is a JSON document by design, which is what lets a
		// parameter's declared type reach the wire. A form-encoded body has
		// no such representation, so reject it rather than smuggle it
		// through as text.
		if !json.Valid([]byte(body)) {
			return upstream.UpstreamConfig{}, fmt.Errorf(
				"%w: body is not valid JSON (form-encoded bodies are not supported)", ErrUnsupportedBody)
		}
		cfg.BodyTemplate = json.RawMessage(body)
	}

	return cfg, nil
}

// MakePathDynamic rewrites one path segment into a {paramName} placeholder
// and declares the matching path parameter. The concrete value from the
// pasted command becomes the parameter's example.
//
// cfg is never mutated, so a rejected call leaves the caller's config intact.
func MakePathDynamic(cfg upstream.UpstreamConfig, segmentIndex int, paramName string) (upstream.UpstreamConfig, error) {
	paramName = strings.TrimSpace(paramName)
	if paramName == "" {
		return cfg, fmt.Errorf("%w: name is empty", ErrInvalidParameterName)
	}

	u, err := url.Parse(cfg.URLTemplate)
	if err != nil {
		return cfg, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}

	trimmed := strings.Trim(u.Path, "/")
	if trimmed == "" {
		return cfg, fmt.Errorf("%w: the path has no segments", ErrPathSegmentRange)
	}
	segments := strings.Split(trimmed, "/")
	if segmentIndex < 0 || segmentIndex >= len(segments) {
		return cfg, fmt.Errorf("%w: index %d, path has %d segment(s)",
			ErrPathSegmentRange, segmentIndex, len(segments))
	}

	// cfg is a copy, but Parameters is a map — its header is shared with the
	// caller, so writing into it directly would mutate their config too.
	params := make(map[string]upstream.ParameterDef, len(cfg.Parameters)+1)
	for k, v := range cfg.Parameters {
		params[k] = v
	}
	if _, exists := params[paramName]; exists {
		return cfg, fmt.Errorf("%w: %q", ErrDuplicateParameter, paramName)
	}

	example := segments[segmentIndex]
	segments[segmentIndex] = "{" + paramName + "}"

	params[paramName] = upstream.ParameterDef{
		Location:     upstream.LocationPath,
		Type:         "string",
		Required:     true,
		ExampleValue: example,
	}

	out := cfg
	// Assembled by hand rather than through u.String(), which would
	// percent-encode the braces just inserted.
	out.URLTemplate = u.Scheme + "://" + u.Host + "/" + strings.Join(segments, "/")
	out.Parameters = params
	return out, nil
}

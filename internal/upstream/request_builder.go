package upstream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"content-pipeline-insider/internal/secrets"
)

var allowedMethods = map[string]bool{
	http.MethodGet:  true,
	http.MethodPost: true,
}

func BuildRequest(
	ctx context.Context,
	config UpstreamConfig,
	paramValues map[string]string,
	resolver secrets.Resolver,
) (*http.Request, error) {
	method := strings.ToUpper(config.Method)
	if method == "" {
		method = http.MethodGet
	}
	if !allowedMethods[method] {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMethod, method)
	}

	resolved, err := ResolveParameters(config.Parameters, paramValues)
	if err != nil {
		return nil, err
	}

	rawURL, err := substitutePath(config.URLTemplate, config.Parameters, resolved)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	q := u.Query()
	for _, qp := range config.QueryParameters {
		q.Set(qp.Name, qp.Value)
	}
	for name, def := range config.Parameters {
		if def.Location != LocationQuery {
			continue
		}
		if val, ok := resolved[name]; ok {
			q.Set(def.wireNameOr(name), val)
		}
	}
	u.RawQuery = q.Encode()

	body, err := substituteBody(config.BodyTemplate, config.Parameters, resolved)
	if err != nil {
		return nil, err
	}

	var req *http.Request
	if body != "" {
		req, err = http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(body))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, u.String(), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	for _, h := range config.Headers {
		switch h.Source {
		case HeaderStatic:
			req.Header.Set(h.Name, h.Value)
		case HeaderSecret:
			secret, err := resolver.Resolve(ctx, h.SecretReference)
			if err != nil {
				return nil, fmt.Errorf("%w: %s: %v", ErrSecretResolution, h.Name, err)
			}
			format := h.Format
			if format == "" {
				format = "{{secret}}"
			}
			req.Header.Set(h.Name, strings.ReplaceAll(format, "{{secret}}", secret))
		default:
			return nil, fmt.Errorf("%w: %s has unknown source %q", ErrInvalidHeader, h.Name, h.Source)
		}
	}

	if err := applyAuthentication(ctx, req, config.Authentication, resolver); err != nil {
		return nil, err
	}

	for name, def := range config.Parameters {
		if def.Location != LocationHeader {
			continue
		}
		if val, ok := resolved[name]; ok {
			req.Header.Set(def.wireNameOr(name), val)
		}
	}

	return req, nil
}

func applyAuthentication(ctx context.Context, req *http.Request, auth *Authentication, resolver secrets.Resolver) error {
	if auth == nil {
		return nil
	}
	secret, err := resolver.Resolve(ctx, auth.SecretReference)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSecretResolution, err)
	}

	switch auth.Type {
	case AuthBearerToken:
		req.Header.Set("Authorization", "Bearer "+secret)
	case AuthBasic:
		// The secret holds "user:password" — curl's -u value verbatim.
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(secret)))
	case AuthAPIKeyHeader:
		if auth.HeaderName == "" {
			return fmt.Errorf("%w: api_key_header requires headerName", ErrInvalidAuth)
		}
		req.Header.Set(auth.HeaderName, secret)
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidAuth, auth.Type)
	}
	return nil
}

// substitutePath replaces every {name} placeholder in the URL template.
func substitutePath(template string, params map[string]ParameterDef, resolved map[string]string) (string, error) {
	result := template
	for name, def := range params {
		if def.Location != LocationPath {
			continue
		}
		placeholder := "{" + name + "}"
		if !strings.Contains(result, placeholder) {
			continue
		}
		val, ok := resolved[name]
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrMissingParameter, name)
		}
		// PathEscape matters: without it a productId of "foo/bar" would
		// inject an extra path segment and reach a different endpoint.
		result = strings.ReplaceAll(result, placeholder, url.PathEscape(val))
	}
	// Catches placeholders whose parameter was never declared — the
	// failure mode when someone renames a Parameters key but forgets
	// the template, or vice versa.
	if strings.Contains(result, "{") && strings.Contains(result, "}") {
		return "", fmt.Errorf("%w: unresolved placeholder in %q", ErrInvalidURL, result)
	}
	return result, nil
}

func substituteBody(template string, params map[string]ParameterDef, resolved map[string]string) (string, error) {
	if template == "" {
		return "", nil
	}
	result := template
	for name, def := range params {
		if def.Location != LocationBody {
			continue
		}
		placeholder := "{" + name + "}"
		if !strings.Contains(result, placeholder) {
			continue
		}
		val, ok := resolved[name]
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrMissingParameter, name)
		}
		encoded, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %v", ErrInvalidParameter, name, err)
		}
		// Strip json.Marshal's surrounding quotes — the template already
		// supplies them, e.g. {"id": "{productId}"}.
		result = strings.ReplaceAll(result, placeholder, strings.Trim(string(encoded), `"`))
	}
	return result, nil
}

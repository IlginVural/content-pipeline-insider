package upstream

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
		switch qp.Source {
		case "", HeaderStatic:
			q.Set(qp.Name, qp.Value)
		case HeaderSecret:
			secret, err := resolver.Resolve(ctx, qp.SecretReference)
			if err != nil {
				return nil, fmt.Errorf("%w: query %s: %v", ErrSecretResolution, qp.Name, err)
			}
			q.Set(qp.Name, secret)
		default:
			return nil, fmt.Errorf("%w: %s has unknown source %q", ErrInvalidQueryParam, qp.Name, qp.Source)
		}
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
	if len(body) > 0 {
		req, err = http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
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

// bodyPlaceholder matches {name} where name is a bare identifier. Every
// such occurrence is treated as an intended substitution point, so a
// typo like {prodctId} is reported rather than shipped as literal text.
// Literal braces in a body template are consequently not supported.
var bodyPlaceholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substituteBody fills a JSON body template with resolved parameters.
//
// The template is decoded, substituted at value positions, and marshalled
// once. It is never treated as text. That is the whole point: splicing
// values into serialized JSON means hand-rolling an escape layer that
// competes with encoding/json, and a value that contains a quote, a
// backslash, or a newline eventually wins that competition. Working on
// the decoded document instead makes malformed output impossible and
// keeps a parameter structurally unable to introduce fields it was never
// declared to fill.
func substituteBody(template json.RawMessage, params map[string]ParameterDef, resolved map[string]string) ([]byte, error) {
	if len(bytes.TrimSpace(template)) == 0 {
		return nil, nil
	}

	// UseNumber so numbers already present in the template keep their
	// exact text — a price of 249.90 must not become 249.9.
	decoder := json.NewDecoder(bytes.NewReader(template))
	decoder.UseNumber()

	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w: unexpected data after the JSON value", ErrInvalidBody)
	}

	substituted, err := substituteValue(document, params, resolved)
	if err != nil {
		return nil, err
	}

	out, err := json.Marshal(substituted)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	return out, nil
}

func substituteValue(value any, params map[string]ParameterDef, resolved map[string]string) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			// Field names are never substituted. A parameter supplies a
			// value; letting one name a field would hand it control of
			// the document's shape, which is exactly the property this
			// design exists to guarantee.
			if bodyPlaceholder.MatchString(key) {
				return nil, fmt.Errorf("%w: a parameter may not name the field %q", ErrInvalidBody, key)
			}
			sub, err := substituteValue(child, params, resolved)
			if err != nil {
				return nil, err
			}
			out[key] = sub
		}
		return out, nil

	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			sub, err := substituteValue(child, params, resolved)
			if err != nil {
				return nil, err
			}
			out[i] = sub
		}
		return out, nil

	case string:
		return substituteString(v, params, resolved)

	default:
		// Numbers, booleans and null carry through untouched.
		return value, nil
	}
}

func substituteString(s string, params map[string]ParameterDef, resolved map[string]string) (any, error) {
	matches := bodyPlaceholder.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s, nil
	}

	// A string that is nothing but a placeholder takes the parameter's
	// declared type: {"quantity":"{qty}"} with qty declared integer sends
	// 5, not "5". This is what makes ParameterDef.Type mean something on
	// the way out as well as on the way in — previously the wire format
	// depended on whether the admin happened to type quotes.
	if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(s) {
		name := s[matches[0][2]:matches[0][3]]
		def, val, err := lookupBodyParameter(name, params, resolved)
		if err != nil {
			return nil, err
		}
		return typedBodyValue(name, def, val)
	}

	// A placeholder inside a longer string interpolates as text, e.g.
	// {"query":"sku:{productId} AND active:true"}. Still safe: the result
	// is a decoded string that json.Marshal escapes on the way out.
	var b strings.Builder
	last := 0
	for _, m := range matches {
		name := s[m[2]:m[3]]
		if _, _, err := lookupBodyParameter(name, params, resolved); err != nil {
			return nil, err
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(resolved[name])
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String(), nil
}

func lookupBodyParameter(name string, params map[string]ParameterDef, resolved map[string]string) (ParameterDef, string, error) {
	def, declared := params[name]
	if !declared || def.Location != LocationBody {
		return ParameterDef{}, "", fmt.Errorf("%w: {%s} is not a declared body parameter", ErrInvalidBody, name)
	}
	val, ok := resolved[name]
	if !ok {
		return ParameterDef{}, "", fmt.Errorf("%w: %s", ErrMissingParameter, name)
	}
	return def, val, nil
}

// typedBodyValue converts a resolved value, which is always a string, into
// the JSON type the parameter was declared to hold. ResolveParameters has
// already validated the text, so a failure here means the two disagree.
func typedBodyValue(name string, def ParameterDef, val string) (any, error) {
	switch def.Type {
	case "integer":
		if _, err := strconv.ParseInt(val, 10, 64); err != nil {
			return nil, fmt.Errorf("%w: %s must be an integer", ErrInvalidParameter, name)
		}
		// json.Number marshals as its literal text, so large ids survive
		// intact instead of being rounded through float64.
		return json.Number(val), nil

	case "number":
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			return nil, fmt.Errorf("%w: %s must be a number", ErrInvalidParameter, name)
		}
		return json.Number(val), nil

	case "boolean":
		parsed, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%w: %s must be a boolean", ErrInvalidParameter, name)
		}
		return parsed, nil

	default:
		return val, nil
	}
}

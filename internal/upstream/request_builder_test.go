package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"content-pipeline-insider/internal/secrets"
)

func TestSubstitutePath(t *testing.T) {
	params := map[string]ParameterDef{
		"productId": {Location: LocationPath, Type: "string", Required: true},
	}

	t.Run("replaces the placeholder", func(t *testing.T) {
		got, err := substitutePath(
			"https://api.partner.com/products/{productId}",
			params,
			map[string]string{"productId": "P123"},
		)
		if err != nil {
			t.Fatalf("substitutePath() = %v", err)
		}
		if want := "https://api.partner.com/products/P123"; got != want {
			t.Fatalf("substitutePath() = %q, want %q", got, want)
		}
	})

	// Without escaping, a value containing a slash silently reaches a
	// different endpoint than the one the administrator approved.
	t.Run("escapes a slash rather than adding a path segment", func(t *testing.T) {
		got, err := substitutePath(
			"https://api.partner.com/products/{productId}",
			params,
			map[string]string{"productId": "../../admin/users"},
		)
		if err != nil {
			t.Fatalf("substitutePath() = %v", err)
		}
		if strings.Contains(got, "/admin/users") {
			t.Fatalf("substitutePath() = %q, want the slashes escaped", got)
		}
		if !strings.Contains(got, "%2F") {
			t.Fatalf("substitutePath() = %q, want percent-encoded slashes", got)
		}
	})

	t.Run("missing value for a declared path parameter", func(t *testing.T) {
		_, err := substitutePath(
			"https://api.partner.com/products/{productId}",
			params,
			map[string]string{},
		)
		if !errors.Is(err, ErrMissingParameter) {
			t.Fatalf("substitutePath() = %v, want %v", err, ErrMissingParameter)
		}
	})

	t.Run("placeholder with no matching parameter", func(t *testing.T) {
		_, err := substitutePath(
			"https://api.partner.com/products/{typoId}",
			params,
			map[string]string{"productId": "P123"},
		)
		if !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("substitutePath() = %v, want %v", err, ErrInvalidURL)
		}
	})
}

// Whatever a value contains, the body must stay parseable JSON and the
// value must survive the round trip. These assert that property rather
// than a particular encoding, so they hold regardless of how escaping is
// implemented underneath.
func TestSubstituteBodyProducesValidJSON(t *testing.T) {
	params := map[string]ParameterDef{
		"productId": {Location: LocationBody, Type: "string", Required: true},
	}
	template := json.RawMessage(`{"id":"{productId}"}`)

	cases := []struct {
		name  string
		value string
	}{
		{"plain value", "P123"},
		{"quote in the middle", `a"b`},
		{"trailing quote", `a"`},
		{"leading quote", `"a`},
		{"only a quote", `"`},
		{"trailing backslash", `a\`},
		{"newline", "a\nb"},
		{"tab", "a\tb"},
		{"unicode", "Ürün — 型番"},
		{"control character", "a\x00b"},
		// A value shaped like JSON punctuation must remain a value. If
		// it could close the string and open a new key, the parameter
		// would be supplying structure rather than data.
		{"json fragment", `","admin":true,"x":"`},
		{"brace soup", `{"a":[1,2]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := substituteBody(template, params, map[string]string{"productId": tc.value})
			if err != nil {
				t.Fatalf("substituteBody() = %v", err)
			}

			var decoded struct {
				ID    string `json:"id"`
				Admin bool   `json:"admin"`
			}
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatalf("substituteBody() produced unparseable JSON %q for value %q: %v", got, tc.value, err)
			}
			if decoded.ID != tc.value {
				t.Errorf("round trip: id = %q, want %q (body was %q)", decoded.ID, tc.value, got)
			}
			if decoded.Admin {
				t.Errorf("value %q injected an extra field into the body %q", tc.value, got)
			}
		})
	}
}

// The declared type decides the JSON type on the wire. Under the previous
// string-splicing design this depended on whether the administrator
// happened to type quotes around the placeholder, with no error either
// way — a partner expecting a number would simply reject the request at
// render time, during a live send.
func TestSubstituteBodyHonoursDeclaredType(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		value    string
		want     string
	}{
		{"integer", "integer", "5", `{"v":5}`},
		{"negative integer", "integer", "-5", `{"v":-5}`},
		// Routed through float64 this would render as 9223372036854776000.
		// "integer" means int64 here, matching validateType.
		{"max int64 keeps every digit", "integer", "9223372036854775807", `{"v":9223372036854775807}`},
		{"number", "number", "249.99", `{"v":249.99}`},
		{"number keeps a trailing zero", "number", "249.90", `{"v":249.90}`},
		{"boolean true", "boolean", "true", `{"v":true}`},
		{"boolean false", "boolean", "false", `{"v":false}`},
		{"string stays quoted", "string", "5", `{"v":"5"}`},
		{"untyped defaults to string", "", "5", `{"v":"5"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]ParameterDef{
				"v": {Location: LocationBody, Type: tc.declared, Required: true},
			}
			got, err := substituteBody(
				json.RawMessage(`{"v":"{v}"}`),
				params,
				map[string]string{"v": tc.value},
			)
			if err != nil {
				t.Fatalf("substituteBody() = %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("substituteBody() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSubstituteBodyStructure(t *testing.T) {
	params := map[string]ParameterDef{
		"productId": {Location: LocationBody, Type: "string", Required: true},
		"qty":       {Location: LocationBody, Type: "integer", Required: true},
	}
	values := map[string]string{"productId": "P123", "qty": "2"}

	t.Run("nested objects and arrays", func(t *testing.T) {
		got, err := substituteBody(
			json.RawMessage(`{"order":{"lines":[{"sku":"{productId}","qty":"{qty}"}]}}`),
			params, values,
		)
		if err != nil {
			t.Fatalf("substituteBody() = %v", err)
		}
		if want := `{"order":{"lines":[{"qty":2,"sku":"P123"}]}}`; string(got) != want {
			t.Errorf("substituteBody() = %s, want %s", got, want)
		}
	})

	t.Run("interpolation inside a longer string", func(t *testing.T) {
		got, err := substituteBody(
			json.RawMessage(`{"query":"sku:{productId} AND active:true"}`),
			params, values,
		)
		if err != nil {
			t.Fatalf("substituteBody() = %v", err)
		}
		if want := `{"query":"sku:P123 AND active:true"}`; string(got) != want {
			t.Errorf("substituteBody() = %s, want %s", got, want)
		}
	})

	t.Run("literal template values are untouched", func(t *testing.T) {
		got, err := substituteBody(
			json.RawMessage(`{"source":"insider","retries":3,"debug":false,"note":null}`),
			params, values,
		)
		if err != nil {
			t.Fatalf("substituteBody() = %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatalf("unparseable: %v", err)
		}
		if decoded["source"] != "insider" || decoded["debug"] != false {
			t.Errorf("substituteBody() = %s, want the literals preserved", got)
		}
	})

	t.Run("empty template produces no body", func(t *testing.T) {
		got, err := substituteBody(nil, params, values)
		if err != nil {
			t.Fatalf("substituteBody() = %v", err)
		}
		if got != nil {
			t.Errorf("substituteBody(nil) = %s, want nil", got)
		}
	})
}

func TestSubstituteBodyRejects(t *testing.T) {
	params := map[string]ParameterDef{
		"productId": {Location: LocationBody, Type: "string", Required: true},
		"locale":    {Location: LocationQuery, Type: "string"},
	}

	cases := []struct {
		name     string
		template string
		resolved map[string]string
		wantErr  error
	}{
		{
			name:     "template is not valid JSON",
			template: `{"id":`,
			resolved: map[string]string{"productId": "P123"},
			wantErr:  ErrInvalidBody,
		},
		{
			// Catches the admin renaming a parameter and forgetting the
			// template, or vice versa.
			name:     "placeholder names nothing declared",
			template: `{"id":"{prodctId}"}`,
			resolved: map[string]string{"productId": "P123"},
			wantErr:  ErrInvalidBody,
		},
		{
			// A query parameter is not a body parameter. Allowing it
			// would let one declaration reach two places at once.
			name:     "placeholder names a non-body parameter",
			template: `{"locale":"{locale}"}`,
			resolved: map[string]string{"productId": "P123", "locale": "en-US"},
			wantErr:  ErrInvalidBody,
		},
		{
			// The property the whole redesign exists to hold.
			name:     "parameter may not name a field",
			template: `{"{productId}":"value"}`,
			resolved: map[string]string{"productId": "P123"},
			wantErr:  ErrInvalidBody,
		},
		{
			name:     "declared parameter has no resolved value",
			template: `{"id":"{productId}"}`,
			resolved: map[string]string{},
			wantErr:  ErrMissingParameter,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := substituteBody(json.RawMessage(tc.template), params, tc.resolved)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("substituteBody() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestBuildRequest(t *testing.T) {
	resolver := secrets.NewMemoryResolver(map[string]string{
		"tenant-45/product-api-token": "secret-token-123",
		"tenant-45/basic":             "alice:hunter2",
	})

	baseConfig := func() UpstreamConfig {
		return UpstreamConfig{
			Method:      "GET",
			URLTemplate: "https://api.partner.com/products/{productId}",
			Parameters: map[string]ParameterDef{
				"productId": {Location: LocationPath, Type: "string", Required: true},
			},
		}
	}

	t.Run("builds a URL, query, and bearer header", func(t *testing.T) {
		cfg := baseConfig()
		cfg.QueryParameters = []QueryParam{{Name: "format", Value: "json"}}
		cfg.Parameters["locale"] = ParameterDef{Location: LocationQuery, Type: "string"}
		cfg.Authentication = &Authentication{
			Type:            AuthBearerToken,
			SecretReference: "tenant-45/product-api-token",
		}

		req, err := BuildRequest(context.Background(), cfg, map[string]string{
			"productId": "P123",
			"locale":    "en-US",
		}, resolver)
		if err != nil {
			t.Fatalf("BuildRequest() = %v", err)
		}

		if req.URL.Path != "/products/P123" {
			t.Errorf("Path = %q, want /products/P123", req.URL.Path)
		}
		if got := req.URL.Query().Get("format"); got != "json" {
			t.Errorf("format = %q, want json", got)
		}
		if got := req.URL.Query().Get("locale"); got != "en-US" {
			t.Errorf("locale = %q, want en-US", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer secret-token-123" {
			t.Errorf("Authorization = %q", got)
		}
	})

	t.Run("basic auth is base64 encoded", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Authentication = &Authentication{Type: AuthBasic, SecretReference: "tenant-45/basic"}

		req, err := BuildRequest(context.Background(), cfg, map[string]string{"productId": "P123"}, resolver)
		if err != nil {
			t.Fatalf("BuildRequest() = %v", err)
		}
		// base64("alice:hunter2")
		if want := "Basic YWxpY2U6aHVudGVyMg=="; req.Header.Get("Authorization") != want {
			t.Errorf("Authorization = %q, want %q", req.Header.Get("Authorization"), want)
		}
	})

	t.Run("api key header requires a header name", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Authentication = &Authentication{
			Type:            AuthAPIKeyHeader,
			SecretReference: "tenant-45/product-api-token",
		}

		_, err := BuildRequest(context.Background(), cfg, map[string]string{"productId": "P123"}, resolver)
		if !errors.Is(err, ErrInvalidAuth) {
			t.Fatalf("BuildRequest() = %v, want %v", err, ErrInvalidAuth)
		}
	})

	t.Run("secret-backed header uses its format", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Headers = []Header{{
			Name:            "X-Api-Key",
			Source:          HeaderSecret,
			SecretReference: "tenant-45/product-api-token",
			Format:          "Key {{secret}}",
		}}

		req, err := BuildRequest(context.Background(), cfg, map[string]string{"productId": "P123"}, resolver)
		if err != nil {
			t.Fatalf("BuildRequest() = %v", err)
		}
		if want := "Key secret-token-123"; req.Header.Get("X-Api-Key") != want {
			t.Errorf("X-Api-Key = %q, want %q", req.Header.Get("X-Api-Key"), want)
		}
	})

	t.Run("unresolvable secret is reported", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Authentication = &Authentication{
			Type:            AuthBearerToken,
			SecretReference: "tenant-45/does-not-exist",
		}

		_, err := BuildRequest(context.Background(), cfg, map[string]string{"productId": "P123"}, resolver)
		if !errors.Is(err, ErrSecretResolution) {
			t.Fatalf("BuildRequest() = %v, want %v", err, ErrSecretResolution)
		}
	})

	// Only GET and POST are allowed. A stored config asking for DELETE
	// would let a read-shaped integration mutate partner state.
	t.Run("methods outside GET and POST are rejected", func(t *testing.T) {
		for _, method := range []string{"PUT", "DELETE", "PATCH", "TRACE"} {
			cfg := baseConfig()
			cfg.Method = method

			_, err := BuildRequest(context.Background(), cfg, map[string]string{"productId": "P123"}, resolver)
			if !errors.Is(err, ErrUnsupportedMethod) {
				t.Errorf("BuildRequest(%s) = %v, want %v", method, err, ErrUnsupportedMethod)
			}
		}
	})

	t.Run("header parameter uses its wire name", func(t *testing.T) {
		cfg := baseConfig()
		cfg.Parameters["customerTier"] = ParameterDef{
			Location: LocationHeader,
			WireName: "X-Customer-Tier",
			Type:     "string",
		}

		req, err := BuildRequest(context.Background(), cfg, map[string]string{
			"productId":    "P123",
			"customerTier": "gold",
		}, resolver)
		if err != nil {
			t.Fatalf("BuildRequest() = %v", err)
		}
		if got := req.Header.Get("X-Customer-Tier"); got != "gold" {
			t.Errorf("X-Customer-Tier = %q, want gold", got)
		}
	})

	t.Run("undeclared parameter is refused", func(t *testing.T) {
		cfg := baseConfig()

		_, err := BuildRequest(context.Background(), cfg, map[string]string{
			"productId": "P123",
			"debug":     "true",
		}, resolver)
		if !errors.Is(err, ErrUnknownParameter) {
			t.Fatalf("BuildRequest() = %v, want %v", err, ErrUnknownParameter)
		}
	})
}

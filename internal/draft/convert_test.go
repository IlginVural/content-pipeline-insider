package draft

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"content-pipeline-insider/internal/curlimport"
	"content-pipeline-insider/internal/secrets"
	"content-pipeline-insider/internal/upstream"
)

func TestFromImported_PlainRequest(t *testing.T) {
	cfg, err := FromImported(context.Background(), curlimport.ImportedRequest{
		Method:  "GET",
		BaseURL: "https://api.partner.com",
		Path:    "/v1/products/42",
		Headers: []curlimport.ImportedHeader{{Name: "Accept", Value: "application/json"}},
		Query:   []curlimport.ImportedParameter{{Name: "locale", Value: "tr"}},
	}, "tenant-1", "draft-1", secrets.NewMemoryResolver(nil))
	if err != nil {
		t.Fatalf("FromImported() error = %v", err)
	}

	if cfg.URLTemplate != "https://api.partner.com/v1/products/42" {
		t.Errorf("URLTemplate = %q", cfg.URLTemplate)
	}
	if cfg.TimeoutMs != DefaultTimeoutMs {
		t.Errorf("TimeoutMs = %d, want %d", cfg.TimeoutMs, DefaultTimeoutMs)
	}
	if len(cfg.Headers) != 1 || cfg.Headers[0].Source != upstream.HeaderStatic {
		t.Fatalf("Headers = %+v, want one static header", cfg.Headers)
	}
	if len(cfg.QueryParameters) != 1 || cfg.QueryParameters[0].Value != "tr" {
		t.Fatalf("QueryParameters = %+v", cfg.QueryParameters)
	}
	if cfg.Authentication != nil {
		t.Errorf("Authentication = %+v, want nil", cfg.Authentication)
	}
}

// Asserting only that a reference exists would pass even if the plaintext
// were still sitting in Value, so the whole config is scanned as well.
func TestFromImported_LiftsSecrets(t *testing.T) {
	const token = "Bearer super-secret-value"

	tests := []struct {
		name      string
		req       curlimport.ImportedRequest
		wantValue string
		refOf     func(upstream.UpstreamConfig) string
	}{
		{
			name: "sensitive header",
			req: curlimport.ImportedRequest{
				Method: "GET", BaseURL: "https://api.partner.com", Path: "/v1/x",
				Headers: []curlimport.ImportedHeader{
					{Name: "Authorization", Value: token, IsSensitive: true},
				},
			},
			wantValue: token,
			refOf:     func(c upstream.UpstreamConfig) string { return c.Headers[0].SecretReference },
		},
		{
			name: "sensitive query parameter",
			req: curlimport.ImportedRequest{
				Method: "GET", BaseURL: "https://api.partner.com", Path: "/v1/x",
				Query: []curlimport.ImportedParameter{
					{Name: "api_key", Value: "k-123", IsSensitive: true},
				},
			},
			wantValue: "k-123",
			refOf:     func(c upstream.UpstreamConfig) string { return c.QueryParameters[0].SecretReference },
		},
		{
			name: "basic auth",
			req: curlimport.ImportedRequest{
				Method: "GET", BaseURL: "https://api.partner.com", Path: "/v1/x",
				Auth: &curlimport.ImportedAuth{Username: "user", Password: "pass"},
			},
			wantValue: "user:pass",
			refOf:     func(c upstream.UpstreamConfig) string { return c.Authentication.SecretReference },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := secrets.NewMemoryResolver(nil)

			cfg, err := FromImported(ctx, tt.req, "tenant-1", "draft-1", store)
			if err != nil {
				t.Fatalf("FromImported() error = %v", err)
			}

			ref := tt.refOf(cfg)
			if ref == "" {
				t.Fatal("no secret reference was set")
			}
			got, err := store.Resolve(ctx, ref)
			if err != nil {
				t.Fatalf("Resolve(%q) error = %v", ref, err)
			}
			if got != tt.wantValue {
				t.Errorf("stored value = %q, want %q", got, tt.wantValue)
			}
			if strings.Contains(dump(t, cfg), tt.wantValue) {
				t.Error("plaintext credential survived into the config")
			}
		})
	}
}

func TestFromImported_Body(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{name: "json object", body: `{"sku":"A1"}`},
		{name: "form encoded", body: "a=1&b=2", wantErr: ErrUnsupportedBody},
		{name: "plain text", body: "hello", wantErr: ErrUnsupportedBody},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FromImported(context.Background(), curlimport.ImportedRequest{
				Method: "POST", BaseURL: "https://api.partner.com", Path: "/v1/x", Body: tt.body,
			}, "tenant-1", "draft-1", secrets.NewMemoryResolver(nil))

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("FromImported() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestFromImported_RejectsUnsupportedMethod(t *testing.T) {
	_, err := FromImported(context.Background(), curlimport.ImportedRequest{
		Method: "DELETE", BaseURL: "https://api.partner.com", Path: "/v1/x",
	}, "tenant-1", "draft-1", secrets.NewMemoryResolver(nil))

	if !errors.Is(err, ErrUnsupportedMethod) {
		t.Fatalf("FromImported() error = %v, want %v", err, ErrUnsupportedMethod)
	}
}

func TestMakePathDynamic(t *testing.T) {
	base := upstream.UpstreamConfig{
		URLTemplate: "https://api.partner.com/v1/products/42",
		Parameters:  map[string]upstream.ParameterDef{},
	}

	got, err := MakePathDynamic(base, 2, "productId")
	if err != nil {
		t.Fatalf("MakePathDynamic() error = %v", err)
	}

	if want := "https://api.partner.com/v1/products/{productId}"; got.URLTemplate != want {
		t.Errorf("URLTemplate = %q, want %q", got.URLTemplate, want)
	}
	def, ok := got.Parameters["productId"]
	if !ok {
		t.Fatal("productId was not declared")
	}
	if def.Location != upstream.LocationPath || !def.Required || def.ExampleValue != "42" {
		t.Errorf("ParameterDef = %+v", def)
	}

	// Parameters is a map, so a naive implementation writes through the copy.
	if base.URLTemplate != "https://api.partner.com/v1/products/42" {
		t.Error("input URLTemplate was mutated")
	}
	if len(base.Parameters) != 0 {
		t.Errorf("input Parameters was mutated: %+v", base.Parameters)
	}
}

func TestMakePathDynamic_Errors(t *testing.T) {
	base := upstream.UpstreamConfig{
		URLTemplate: "https://api.partner.com/v1/products/42",
		Parameters:  map[string]upstream.ParameterDef{"productId": {}},
	}

	tests := []struct {
		name    string
		index   int
		param   string
		wantErr error
	}{
		{name: "index too high", index: 9, param: "x", wantErr: ErrPathSegmentRange},
		{name: "negative index", index: -1, param: "x", wantErr: ErrPathSegmentRange},
		{name: "empty name", index: 2, param: "  ", wantErr: ErrInvalidParameterName},
		{name: "already declared", index: 2, param: "productId", wantErr: ErrDuplicateParameter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := MakePathDynamic(base, tt.index, tt.param); !errors.Is(err, tt.wantErr) {
				t.Fatalf("MakePathDynamic() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func dump(t *testing.T, cfg upstream.UpstreamConfig) string {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(b)
}

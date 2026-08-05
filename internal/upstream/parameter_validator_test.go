package upstream

import (
	"errors"
	"testing"
)

func TestResolveParameters(t *testing.T) {
	t.Run("required parameter missing", func(t *testing.T) {
		params := map[string]ParameterDef{
			"productId": {Location: LocationPath, Type: "string", Required: true},
		}
		_, err := ResolveParameters(params, map[string]string{})
		if !errors.Is(err, ErrMissingParameter) {
			t.Fatalf("ResolveParameters() = %v, want %v", err, ErrMissingParameter)
		}
	})

	t.Run("optional parameter falls back to its default", func(t *testing.T) {
		params := map[string]ParameterDef{
			"locale": {Location: LocationQuery, Type: "string", Default: "en-US"},
		}
		resolved, err := ResolveParameters(params, map[string]string{})
		if err != nil {
			t.Fatalf("ResolveParameters() = %v", err)
		}
		if resolved["locale"] != "en-US" {
			t.Errorf("locale = %q, want en-US", resolved["locale"])
		}
	})

	t.Run("optional parameter with no default is omitted", func(t *testing.T) {
		params := map[string]ParameterDef{
			"locale": {Location: LocationQuery, Type: "string"},
		}
		resolved, err := ResolveParameters(params, map[string]string{})
		if err != nil {
			t.Fatalf("ResolveParameters() = %v", err)
		}
		if _, present := resolved["locale"]; present {
			t.Errorf("locale present as %q, want it omitted entirely", resolved["locale"])
		}
	})

	// The heart of the design: a marketer may supply values only for
	// parameters an administrator declared. An undeclared name is not
	// ignored, it is an error — silently dropping it would make the
	// request differ from what the caller believes it sent.
	t.Run("undeclared parameter is rejected", func(t *testing.T) {
		params := map[string]ParameterDef{
			"productId": {Location: LocationPath, Type: "string", Required: true},
		}
		values := map[string]string{"productId": "P123", "internalHost": "127.0.0.1"}

		_, err := ResolveParameters(params, values)
		if !errors.Is(err, ErrUnknownParameter) {
			t.Fatalf("ResolveParameters() = %v, want %v", err, ErrUnknownParameter)
		}
	})
}

func TestResolveParametersValidation(t *testing.T) {
	cases := []struct {
		name    string
		def     ParameterDef
		value   string
		wantErr error
	}{
		{
			name:  "pattern matches",
			def:   ParameterDef{Type: "string", Validation: &Validation{Pattern: `^[A-Za-z0-9_-]+$`}},
			value: "P123",
		},
		{
			name:    "pattern does not match",
			def:     ParameterDef{Type: "string", Validation: &Validation{Pattern: `^[A-Za-z0-9_-]+$`}},
			value:   "../../etc/passwd",
			wantErr: ErrInvalidParameter,
		},
		{
			name:    "invalid pattern is reported, not ignored",
			def:     ParameterDef{Type: "string", Validation: &Validation{Pattern: `^[a-z`}},
			value:   "anything",
			wantErr: ErrInvalidParameter,
		},
		{
			name:  "within maximum length",
			def:   ParameterDef{Type: "string", Validation: &Validation{MaximumLength: 5}},
			value: "P1234",
		},
		{
			// Counted in characters, not bytes. Each of these is one
			// character but several bytes; counting bytes made the
			// effective limit depend on the alphabet.
			name:  "multi-byte characters count as one each",
			def:   ParameterDef{Type: "string", Validation: &Validation{MaximumLength: 5}},
			value: "Ürünü",
		},
		{
			name:    "multi-byte characters still hit the limit",
			def:     ParameterDef{Type: "string", Validation: &Validation{MaximumLength: 5}},
			value:   "Ürünlerimiz",
			wantErr: ErrInvalidParameter,
		},
		{
			name:    "exceeds maximum length",
			def:     ParameterDef{Type: "string", Validation: &Validation{MaximumLength: 5}},
			value:   "P123456",
			wantErr: ErrInvalidParameter,
		},
		{
			name:  "allowed value",
			def:   ParameterDef{Type: "string", AllowedValues: []string{"en-US", "tr-TR"}},
			value: "tr-TR",
		},
		{
			name:    "disallowed value",
			def:     ParameterDef{Type: "string", AllowedValues: []string{"en-US", "tr-TR"}},
			value:   "de-DE",
			wantErr: ErrInvalidParameter,
		},
		{name: "empty type behaves as string", def: ParameterDef{}, value: "anything"},
		{name: "integer accepts digits", def: ParameterDef{Type: "integer"}, value: "42"},
		{name: "integer accepts negatives", def: ParameterDef{Type: "integer"}, value: "-42"},
		{
			name:    "integer rejects letters",
			def:     ParameterDef{Type: "integer"},
			value:   "42abc",
			wantErr: ErrInvalidParameter,
		},
		{
			name:    "integer rejects a decimal",
			def:     ParameterDef{Type: "integer"},
			value:   "4.2",
			wantErr: ErrInvalidParameter,
		},
		{name: "number accepts a decimal", def: ParameterDef{Type: "number"}, value: "249.99"},
		{
			name:    "number rejects text",
			def:     ParameterDef{Type: "number"},
			value:   "cheap",
			wantErr: ErrInvalidParameter,
		},
		{name: "boolean accepts true", def: ParameterDef{Type: "boolean"}, value: "true"},
		{
			name:    "boolean rejects yes",
			def:     ParameterDef{Type: "boolean"},
			value:   "yes",
			wantErr: ErrInvalidParameter,
		},
		{
			name:    "unknown type is rejected",
			def:     ParameterDef{Type: "date"},
			value:   "2026-08-05",
			wantErr: ErrInvalidParameter,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := tc.def
			def.Required = true
			params := map[string]ParameterDef{"p": def}

			_, err := ResolveParameters(params, map[string]string{"p": tc.value})
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ResolveParameters() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ResolveParameters() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

package upstream

import "encoding/json"

// UpstreamConfig is the persisted wayfor calling a partner API.

type UpstreamConfig struct {
	Method      string `json:"method"`
	URLTemplate string `json:"urlTemplate"`

	// BodyTemplate is a JSON document, not text. Placeholders live at
	// value positions inside it and are substituted into the decoded
	// document, which is then marshalled once. Keeping it structured is
	// what makes escaping json.Marshal's problem rather than ours, and
	// what lets a parameter's declared type reach the wire.
	BodyTemplate json.RawMessage `json:"bodyTemplate,omitempty"`

	Headers         []Header                `json:"headers,omitempty"`
	QueryParameters []QueryParam            `json:"queryParameters,omitempty"`
	Parameters      map[string]ParameterDef `json:"parameters,omitempty"`
	Authentication  *Authentication         `json:"authentication,omitempty"`
	TimeoutMs       int                     `json:"timeoutMs,omitempty"`
}

type AuthType string

const (
	AuthBearerToken  AuthType = "bearer_token"
	AuthBasic        AuthType = "basic"
	AuthAPIKeyHeader AuthType = "api_key_header"
)

type Authentication struct {
	Type            AuthType `json:"type"`
	SecretReference string   `json:"secretReference"`
	HeaderName      string   `json:"headerName,omitempty"`
}

type HeaderSource string

const (
	HeaderStatic HeaderSource = "static"
	HeaderSecret HeaderSource = "secret"
)

type Header struct {
	Name            string       `json:"name"`
	Source          HeaderSource `json:"source"`
	Value           string       `json:"value,omitempty"`
	SecretReference string       `json:"secretReference,omitempty"`
	Format          string       `json:"format,omitempty"`
}

type QueryParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ParameterLocation string

const (
	LocationPath   ParameterLocation = "path"
	LocationQuery  ParameterLocation = "query"
	LocationHeader ParameterLocation = "header"
	LocationBody   ParameterLocation = "body"
)

type ParameterDef struct {
	Location      ParameterLocation `json:"location"`
	WireName      string            `json:"wireName,omitempty"`
	Type          string            `json:"type"`
	Required      bool              `json:"required"`
	Default       string            `json:"default,omitempty"`
	ExampleValue  string            `json:"exampleValue,omitempty"`
	AllowedValues []string          `json:"allowedValues,omitempty"`
	Validation    *Validation       `json:"validation,omitempty"`
}

func (p ParameterDef) wireNameOr(paramName string) string {
	if p.WireName != "" {
		return p.WireName
	}
	return paramName
}

type Validation struct {
	Pattern       string `json:"pattern,omitempty"`
	MaximumLength int    `json:"maximumLength,omitempty"`
}

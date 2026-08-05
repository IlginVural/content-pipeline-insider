package curlimport

import "strings"

// JSON field names match the import response documented in the README.
type ImportedRequest struct {
	Method  string              `json:"method"`
	BaseURL string              `json:"baseUrl"`
	Path    string              `json:"path"`
	Query   []ImportedParameter `json:"queryParameters,omitempty"`
	Headers []ImportedHeader    `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
	Auth    *ImportedAuth       `json:"auth,omitempty"`
}

type ImportedHeader struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	IsSensitive bool   `json:"sensitive"`
}

type ImportedParameter struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	IsSensitive bool   `json:"sensitive"`
}

type ImportedAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r ImportedRequest) PathSegments() []string {
	trimmed := strings.Trim(r.Path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

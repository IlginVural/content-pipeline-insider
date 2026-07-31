package upstream

// making HTTP request to the external/partner APIs

type UpstreamConfig struct {
	Method          string // HTTP method (GET, POST, etc.)
	URLTemplate     string
	Headers         []Header
	QueryParameters []QueryParam
	Parameters      map[string]ParameterDef
}

type HeaderSource string

const (
	HeaderStatic HeaderSource = "static"
	HeaderSecret HeaderSource = "secret"
)

type Header struct {
	Name            string
	Source          HeaderSource
	Value           string
	SecretReference string
	Format          string
}

type QueryParam struct {
	Name  string
	Value string
}

type ParameterLocation string

const (
	LocationPath   ParameterLocation = "path"
	LocationQuery  ParameterLocation = "query"
	LocationHeader ParameterLocation = "header"
)

type ParameterDef struct {
	Location      ParameterLocation
	Type          string
	Required      bool
	Default       string
	AllowedValues []string
	Pattern       string
	MaximumLength int
}

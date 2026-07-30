package curlimport

type ImportedRequest struct{
	Method string
	BaseURL string
	Path string
	Query []ImportedParameter
	Headers []ImportedHeader
	Body string
	Auth *ImportedAuth
}

type ImportedHeader struct{
	Name string
	Value string
	IsSensitive bool
}

type ImportedParameter struct{
	Name string
	Value string
}

type ImportedAuth struct{
	Username string
	Password string
}
package curlimport

import "strings"

// allowedFlags is every flag this MVP importer understands. Anything
// not listed here is rejected — either because it's denylisted below,
// or simply because we haven't parsed it yet. 
var allowedFlags = map[string]bool{
	"-X": true, "--request": true,
	"-H": true, "--header": true,
	"-u": true, "--user": true,
	"--url": true,
	"-G": true, "--get": true,
	"-d": true, "--data": true, "--data-raw": true, "--data-urlencode": true,
}

// deniedFlags are never allowed, regardless of MVP scope, because they
// let the command read local files, change TLS/proxy behavior, or
// otherwise step outside "describe one HTTP request".
var deniedFlags = map[string]bool{
	"--config": true, "-K": true,
	"--proxy": true, "-x": true,
	"--unix-socket": true,
	"--resolve":     true,
	"--connect-to":  true,
	"--cert": true, "-E": true,
	"--key":         true,
	"--insecure":    true, "-k": true,
	"--upload-file": true, "-T": true,
}

// flagsWithValue lists flags that consume the next token as their
// value, so the parser knows to advance past it.
var flagsWithValue = map[string]bool{
	"-X": true, "--request": true,
	"-H": true, "--header": true,
	"-u": true, "--user": true,
	"--url": true,
	"-d": true, "--data": true, "--data-raw": true, "--data-urlencode": true,
}

func classifyFlag(tok string) (allowed, denied, takesValue bool) {
	return allowedFlags[tok], deniedFlags[tok], flagsWithValue[tok]
}

// sensitiveHeaderNames marks headers whose values should be flagged for
// secret-storage handling by the caller. 
var sensitiveHeaderNames = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"api-key":             true,
	"x-auth-token":        true,
}

func isSensitiveHeader(name string) bool {
	return sensitiveHeaderNames[strings.ToLower(name)]
}
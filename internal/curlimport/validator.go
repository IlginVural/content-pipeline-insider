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
	"-G":    true, "--get": true,
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
	"--cert":        true, "-E": true,
	"--key":      true,
	"--insecure": true, "-k": true,
	"--upload-file": true, "-T": true,
}

// flagsWithValue lists flags that consume the next token as their
// value, so the parser knows to advance past it.
var flagsWithValue = map[string]bool{
	"-X": true, "--request": true,
	"-H": true, "--header": true,
	"-u": true, "--user": true,
	"--url": true,
	"-d":    true, "--data": true, "--data-raw": true, "--data-urlencode": true,
}

func classifyFlag(tok string) (allowed, denied, takesValue bool) {
	return allowedFlags[tok], deniedFlags[tok], flagsWithValue[tok]
}

// nonSensitiveHeaderNames is the set of headers known to carry no
// credential. Everything outside it is treated as sensitive.
//
// This is an allowlist rather than a list of credential headers, because
// the set of credential header names is open and the set of boring
// standard ones is not. Partners authenticate with X-Partner-Secret,
// X-Amz-Security-Token, X-Shopify-Access-Token, X-Vault-Token and
// whatever they invent next; no enumeration keeps up.
//
// The failure costs decide the direction. Classifying a credential as
// ordinary writes a live token into PostgreSQL and its backups, and the
// only remedy is rotating it at the partner. Classifying Accept as a
// credential shows the administrator one wrong checkbox. Guess in the
// direction of the cheap mistake.
var nonSensitiveHeaderNames = map[string]bool{
	"accept":          true,
	"accept-charset":  true,
	"accept-encoding": true,
	"accept-language": true,
	"cache-control":   true,
	"connection":      true,
	"content-length":  true,
	"content-type":    true,
	"expect":          true,
	"host":            true,
	"pragma":          true,
	"te":              true,
	"user-agent":      true,
}

// isSensitiveHeader reports whether a header's value must be routed to
// the secret store rather than persisted alongside the configuration.
//
// Referer and Origin are deliberately absent from the allowlist: a
// referring URL can carry a token in its query string.
func isSensitiveHeader(name string) bool {
	return !nonSensitiveHeaderNames[strings.ToLower(name)]
}

// credentialMarkers appear in the names of query parameters that carry a
// credential: api_key, access_token, client_secret, sig, X-Amz-Signature
// and their many spellings.
var credentialMarkers = []string{
	"apikey",
	"token",
	"secret",
	"password",
	"passwd",
	"signature",
	"credential",
	"auth",
	"session",
}

// shortCredentialNames are matched whole rather than as substrings. "key"
// inside "monkey" or "sig" inside a longer word would be noise.
var shortCredentialNames = map[string]bool{
	"key": true,
	"sig": true,
	"pwd": true,
}

// isSensitiveQueryParam reports whether a query parameter's name suggests
// it carries a credential.
//
// Query strings are handled by name pattern rather than by the allowlist
// inversion used for headers, because the two populations differ. Almost
// every query parameter is ordinary data — locale, page, currency, sort —
// so defaulting them all to sensitive would flag noise on every import
// and train administrators to click through the warning. Headers are the
// opposite: a non-standard header is far more likely to be authentication
// than not.
//
// This is still a guess, and it is the administrator's review before
// publishing that makes it safe. What it must not do is stay silent,
// which is what it did before: a credential in a query string reached
// API responses and audit logs in plaintext.
func isSensitiveQueryParam(name string) bool {
	normalized := strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', '.', ' ':
			return -1
		}
		return r
	}, strings.ToLower(name))

	if shortCredentialNames[normalized] {
		return true
	}
	for _, marker := range credentialMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

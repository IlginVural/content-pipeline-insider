package curlimport

// Mask is what a sensitive value is replaced with in any representation
// that leaves the backend — API responses and audit records alike.
const Mask = "••••••••••••"

// Redacted returns a copy safe to serialize outward. The receiver is
// unchanged, so the caller still holds the real values for the
// extraction step that moves them into the secret store.
//
// This exists because ImportedRequest deliberately carries plaintext
// credentials in memory — it has to, it just parsed them out of the
// cURL — and there is exactly one moment where that is acceptable:
// between parsing and secret storage. Anything crossing an API boundary
// or landing in an audit log goes through here first.
func (r ImportedRequest) Redacted() ImportedRequest {
	out := r

	out.Headers = make([]ImportedHeader, len(r.Headers))
	for i, h := range r.Headers {
		if h.IsSensitive {
			h.Value = Mask
		}
		out.Headers[i] = h
	}

	if r.Auth != nil {
		out.Auth = &ImportedAuth{Username: r.Auth.Username, Password: Mask}
	}
	return out
}

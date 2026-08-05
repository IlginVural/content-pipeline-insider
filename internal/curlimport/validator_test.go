package curlimport

import "testing"

func TestIsSensitiveHeader(t *testing.T) {
	t.Run("known-safe headers are not sensitive", func(t *testing.T) {
		safe := []string{
			"Accept",
			"Accept-Encoding",
			"Accept-Language",
			"Content-Type",
			"Cache-Control",
			"User-Agent",
			"Host",
		}
		for _, name := range safe {
			if isSensitiveHeader(name) {
				t.Errorf("isSensitiveHeader(%q) = true, want false", name)
			}
		}
	})

	t.Run("classification is case insensitive", func(t *testing.T) {
		for _, name := range []string{"accept", "ACCEPT", "AcCePt"} {
			if isSensitiveHeader(name) {
				t.Errorf("isSensitiveHeader(%q) = true, want false", name)
			}
		}
		for _, name := range []string{"authorization", "AUTHORIZATION", "AuThOrIzAtIoN"} {
			if !isSensitiveHeader(name) {
				t.Errorf("isSensitiveHeader(%q) = false, want true", name)
			}
		}
	})

	t.Run("well-known credential headers are sensitive", func(t *testing.T) {
		credentials := []string{
			"Authorization",
			"Cookie",
			"Proxy-Authorization",
			"X-Api-Key",
			"Api-Key",
			"X-Auth-Token",
		}
		for _, name := range credentials {
			if !isSensitiveHeader(name) {
				t.Errorf("isSensitiveHeader(%q) = false, want true", name)
			}
		}
	})

	// The reason the classification is an allowlist. None of these appear
	// on any denylist anyone would think to write, and every one of them
	// carries a live credential.
	t.Run("partner-specific credential headers are sensitive", func(t *testing.T) {
		credentials := []string{
			"X-Partner-Secret",
			"X-Amz-Security-Token",
			"X-Shopify-Access-Token",
			"X-Vault-Token",
			"X-Algolia-API-Key",
			"X-Figma-Token",
			"Private-Token",
			"X-Whatever-They-Invent-Next",
		}
		for _, name := range credentials {
			if !isSensitiveHeader(name) {
				t.Errorf("isSensitiveHeader(%q) = false, want true", name)
			}
		}
	})

	// Referer can carry a token in its query string, so it is not on the
	// allowlist even though it is a standard header.
	t.Run("referer and origin are treated as sensitive", func(t *testing.T) {
		for _, name := range []string{"Referer", "Origin"} {
			if !isSensitiveHeader(name) {
				t.Errorf("isSensitiveHeader(%q) = false, want true", name)
			}
		}
	})
}

func TestIsSensitiveQueryParam(t *testing.T) {
	t.Run("credential names in their many spellings", func(t *testing.T) {
		credentials := []string{
			"api_key", "apikey", "api-key", "API_KEY",
			"key", "Key",
			"token", "access_token", "accessToken", "refresh_token", "id_token",
			"secret", "client_secret",
			"password", "passwd", "pwd",
			"signature", "sig", "X-Amz-Signature",
			"auth", "authorization",
			"credential", "credentials",
			"session", "sessionid", "session_id",
		}
		for _, name := range credentials {
			if !isSensitiveQueryParam(name) {
				t.Errorf("isSensitiveQueryParam(%q) = false, want true", name)
			}
		}
	})

	// The other half of the contract. Flagging ordinary parameters would
	// put a warning on every import and train admins to ignore it.
	t.Run("ordinary parameters are not flagged", func(t *testing.T) {
		ordinary := []string{
			"locale", "page", "limit", "offset", "sort", "order",
			"currency", "country", "lang", "format", "fields",
			"product_id", "productId", "sku", "category",
			"start_date", "end_date", "timezone", "version",
		}
		for _, name := range ordinary {
			if isSensitiveQueryParam(name) {
				t.Errorf("isSensitiveQueryParam(%q) = true, want false", name)
			}
		}
	})
}

// End to end through Parse and Redacted: a query-string credential must
// not survive redaction in plaintext.
func TestQueryCredentialIsRedacted(t *testing.T) {
	req, err := Parse(`curl 'https://api.partner.com/products/P123?api_key=tok-CCC&locale=en-US'`)
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}

	byName := map[string]ImportedParameter{}
	for _, q := range req.Redacted().Query {
		byName[q.Name] = q
	}

	apiKey := byName["api_key"]
	if !apiKey.IsSensitive {
		t.Error("api_key is not marked sensitive")
	}
	if apiKey.Value != Mask {
		t.Errorf("api_key = %q in redacted output, want it masked", apiKey.Value)
	}

	if got := byName["locale"].Value; got != "en-US" {
		t.Errorf("locale = %q, want it left readable", got)
	}

	// The caller still holds the real value for the step that moves it
	// into the secret store.
	for _, q := range req.Query {
		if q.Name == "api_key" && q.Value != "tok-CCC" {
			t.Errorf("receiver mutated: api_key = %q", q.Value)
		}
	}
}

// End to end through Parse and Redacted: a custom auth header must not
// survive redaction in plaintext, because the redacted form is what
// reaches API responses and audit logs.
func TestCustomAuthHeaderIsRedacted(t *testing.T) {
	req, err := Parse(`curl 'https://api.partner.com/products/P123' ` +
		`-H 'X-Partner-Secret: tok-BBB' ` +
		`-H 'Accept: application/json'`)
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}

	byName := map[string]ImportedHeader{}
	for _, h := range req.Redacted().Headers {
		byName[h.Name] = h
	}

	secret := byName["X-Partner-Secret"]
	if !secret.IsSensitive {
		t.Error("X-Partner-Secret is not marked sensitive")
	}
	if secret.Value != Mask {
		t.Errorf("X-Partner-Secret = %q in redacted output, want it masked", secret.Value)
	}

	// The admin still needs to see the ordinary headers to confirm the
	// import is the command they meant to paste.
	if got := byName["Accept"].Value; got != "application/json" {
		t.Errorf("Accept = %q, want it left readable", got)
	}
}

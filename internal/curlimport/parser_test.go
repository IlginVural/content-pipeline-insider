package curlimport

import (
	"errors"
	"strings"
	"testing"
)

func TestParseREADMEExample(t *testing.T) {
	// The exact command the README documents, so a change to the
	// importer that contradicts the documentation fails here.
	const command = `curl --request GET ` +
		`'https://api.partner.com/products/P123?locale=en-US' ` +
		`--header 'Authorization: Bearer secret-token-123' ` +
		`--header 'Accept: application/json'`

	req, err := Parse(command)
	if err != nil {
		t.Fatalf("Parse() = %v, want nil", err)
	}

	if req.Method != "GET" {
		t.Errorf("Method = %q, want GET", req.Method)
	}
	if req.BaseURL != "https://api.partner.com" {
		t.Errorf("BaseURL = %q, want https://api.partner.com", req.BaseURL)
	}
	if req.Path != "/products/P123" {
		t.Errorf("Path = %q, want /products/P123", req.Path)
	}

	if len(req.Query) != 1 || req.Query[0].Name != "locale" || req.Query[0].Value != "en-US" {
		t.Errorf("Query = %#v, want one locale=en-US", req.Query)
	}

	if len(req.Headers) != 2 {
		t.Fatalf("Headers = %#v, want 2", req.Headers)
	}
	byName := map[string]ImportedHeader{}
	for _, h := range req.Headers {
		byName[h.Name] = h
	}
	if auth := byName["Authorization"]; auth.Value != "Bearer secret-token-123" || !auth.IsSensitive {
		t.Errorf("Authorization = %#v, want the token marked sensitive", auth)
	}
	if accept := byName["Accept"]; accept.Value != "application/json" || accept.IsSensitive {
		t.Errorf("Accept = %#v, want application/json not sensitive", accept)
	}
}

func TestParseMethodAndBody(t *testing.T) {
	t.Run("explicit method is uppercased", func(t *testing.T) {
		req, err := Parse(`curl -X post https://api.partner.com/x`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Method != "POST" {
			t.Errorf("Method = %q, want POST", req.Method)
		}
	})

	t.Run("method defaults to GET", func(t *testing.T) {
		req, err := Parse(`curl https://api.partner.com/x`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Method != "GET" {
			t.Errorf("Method = %q, want GET", req.Method)
		}
	})

	t.Run("data implies POST", func(t *testing.T) {
		req, err := Parse(`curl https://api.partner.com/x -d 'a=1'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Method != "POST" {
			t.Errorf("Method = %q, want POST", req.Method)
		}
		if req.Body != "a=1" {
			t.Errorf("Body = %q, want a=1", req.Body)
		}
	})

	t.Run("repeated data flags are joined", func(t *testing.T) {
		req, err := Parse(`curl https://api.partner.com/x -d 'a=1' -d 'b=2'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Body != "a=1&b=2" {
			t.Errorf("Body = %q, want a=1&b=2", req.Body)
		}
	})

	t.Run("-G moves data into the query string", func(t *testing.T) {
		req, err := Parse(`curl -G 'https://api.partner.com/x?a=1' -d 'b=2'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Method != "GET" {
			t.Errorf("Method = %q, want GET", req.Method)
		}
		if req.Body != "" {
			t.Errorf("Body = %q, want empty — -G sends data as query", req.Body)
		}
		if len(req.Query) != 2 {
			t.Fatalf("Query = %#v, want 2 entries", req.Query)
		}
		// Query is sorted so the parse result is stable across runs;
		// Go map iteration is randomized and would otherwise reorder it.
		if req.Query[0].Name != "a" || req.Query[1].Name != "b" {
			t.Errorf("Query = %#v, want sorted a then b", req.Query)
		}
	})
}

// Credentials in the URL were silently discarded, because u.Host omits
// the userinfo section. The admin pasted a working command and got a
// configuration that failed against the partner with no explanation.
func TestParseURLCredentials(t *testing.T) {
	t.Run("userinfo becomes auth", func(t *testing.T) {
		req, err := Parse(`curl 'https://alice:hunter2@api.partner.com/products'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Auth == nil {
			t.Fatal("Auth = nil, want credentials taken from the URL")
		}
		if req.Auth.Username != "alice" || req.Auth.Password != "hunter2" {
			t.Errorf("Auth = %+v, want alice/hunter2", req.Auth)
		}
		// The credentials must not remain in the stored URL.
		if strings.Contains(req.BaseURL, "hunter2") {
			t.Errorf("BaseURL = %q, want the credentials removed", req.BaseURL)
		}
	})

	t.Run("username with no password", func(t *testing.T) {
		req, err := Parse(`curl 'https://alice@api.partner.com/products'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Auth == nil || req.Auth.Username != "alice" || req.Auth.Password != "" {
			t.Errorf("Auth = %+v, want alice with an empty password", req.Auth)
		}
	})

	// curl's own precedence: an explicit -u wins over the URL.
	t.Run("explicit -u wins over URL credentials", func(t *testing.T) {
		req, err := Parse(`curl -u 'bob:s3cret' 'https://alice:hunter2@api.partner.com/products'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Auth.Username != "bob" || req.Auth.Password != "s3cret" {
			t.Errorf("Auth = %+v, want bob/s3cret", req.Auth)
		}
	})

	t.Run("no credentials means no auth block", func(t *testing.T) {
		req, err := Parse(`curl 'https://api.partner.com/products'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Auth != nil {
			t.Errorf("Auth = %+v, want nil", req.Auth)
		}
	})

	t.Run("URL credentials are masked by Redacted", func(t *testing.T) {
		req, err := Parse(`curl 'https://alice:hunter2@api.partner.com/products'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if got := req.Redacted().Auth.Password; got != Mask {
			t.Errorf("redacted password = %q, want it masked", got)
		}
	})
}

// --data-urlencode percent-encodes its content; -d does not. Treating
// them identically stored a request that differed from what curl sends.
func TestParseDataURLEncode(t *testing.T) {
	t.Run("value is encoded, name is not", func(t *testing.T) {
		req, err := Parse(`curl 'https://api.partner.com/x' --data-urlencode 'note=a b&c'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Body != "note=a+b%26c" {
			t.Errorf("Body = %q, want note=a+b%%26c", req.Body)
		}
	})

	t.Run("bare value is encoded whole", func(t *testing.T) {
		req, err := Parse(`curl 'https://api.partner.com/x' --data-urlencode 'a b'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Body != "a+b" {
			t.Errorf("Body = %q, want a+b", req.Body)
		}
	})

	t.Run("plain -d is left raw", func(t *testing.T) {
		req, err := Parse(`curl 'https://api.partner.com/x' -d 'note=a b'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.Body != "note=a b" {
			t.Errorf("Body = %q, want it unencoded", req.Body)
		}
	})

	t.Run("round trips through -G into the query", func(t *testing.T) {
		req, err := Parse(`curl -G 'https://api.partner.com/x' --data-urlencode 'note=a b'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if len(req.Query) != 1 {
			t.Fatalf("Query = %#v, want one entry", req.Query)
		}
		if req.Query[0].Name != "note" || req.Query[0].Value != "a b" {
			t.Errorf("Query[0] = %+v, want note=\"a b\" decoded", req.Query[0])
		}
	})

	t.Run("file references are still refused", func(t *testing.T) {
		_, err := Parse(`curl 'https://api.partner.com/x' --data-urlencode '@/etc/passwd'`)
		if !errors.Is(err, ErrDangerousFlag) {
			t.Fatalf("Parse() = %v, want %v", err, ErrDangerousFlag)
		}
	})
}

func TestParseURLForms(t *testing.T) {
	t.Run("positional URL anywhere in the arguments", func(t *testing.T) {
		req, err := Parse(`curl -X GET https://api.partner.com/x -H 'Accept: application/json'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.BaseURL != "https://api.partner.com" || req.Path != "/x" {
			t.Errorf("BaseURL/Path = %q %q", req.BaseURL, req.Path)
		}
	})

	t.Run("--url flag", func(t *testing.T) {
		req, err := Parse(`curl --url 'https://api.partner.com/x'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		if req.BaseURL != "https://api.partner.com" {
			t.Errorf("BaseURL = %q", req.BaseURL)
		}
	})

	t.Run("query parameters are sorted", func(t *testing.T) {
		req, err := Parse(`curl 'https://api.partner.com/x?z=1&a=2&m=3'`)
		if err != nil {
			t.Fatalf("Parse() = %v", err)
		}
		want := []string{"a", "m", "z"}
		if len(req.Query) != len(want) {
			t.Fatalf("Query = %#v, want %d entries", req.Query, len(want))
		}
		for i, name := range want {
			if req.Query[i].Name != name {
				t.Errorf("Query[%d].Name = %q, want %q", i, req.Query[i].Name, name)
			}
		}
	})
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name    string
		command string
		wantErr error
	}{
		{"empty command", ``, ErrEmptyCommand},
		{"not curl", `wget https://api.partner.com`, ErrNotCurl},
		{"no URL", `curl -X POST`, ErrMissingURL},
		{"missing flag value", `curl https://api.partner.com -H`, ErrMissingFlagValue},
		{"header without a colon", `curl https://api.partner.com -H 'NotAHeader'`, ErrInvalidHeader},
		{"user without a colon", `curl https://api.partner.com -u nopassword`, ErrInvalidUser},

		// Flags that would read local files, weaken TLS, or redirect the
		// connection somewhere the platform never approved.
		{"proxy", `curl --proxy http://evil https://api.partner.com`, ErrDangerousFlag},
		{"short proxy", `curl -x http://evil https://api.partner.com`, ErrDangerousFlag},
		{"insecure", `curl -k https://api.partner.com`, ErrDangerousFlag},
		{"config file", `curl -K /etc/curlrc https://api.partner.com`, ErrDangerousFlag},
		{"unix socket", `curl --unix-socket /var/run/docker.sock https://api.partner.com`, ErrDangerousFlag},
		{"custom DNS resolution", `curl --resolve api.partner.com:443:127.0.0.1 https://api.partner.com`, ErrDangerousFlag},
		{"connect-to", `curl --connect-to ::127.0.0.1: https://api.partner.com`, ErrDangerousFlag},
		{"client certificate", `curl -E /etc/ssl/client.pem https://api.partner.com`, ErrDangerousFlag},
		{"upload file", `curl -T /etc/passwd https://api.partner.com`, ErrDangerousFlag},
		{"data from a file", `curl https://api.partner.com -d @/etc/passwd`, ErrDangerousFlag},
		{"file URL", `curl 'file:///etc/passwd'`, ErrDangerousFlag},

		// Not dangerous, just outside what this MVP parses. Rejecting is
		// still correct: silently ignoring a flag would store a request
		// that differs from the one the admin tested.
		{"unsupported flag", `curl --compressed https://api.partner.com`, ErrUnsupportedFlag},
		{"unsupported output flag", `curl -o /tmp/out https://api.partner.com`, ErrUnsupportedFlag},

		{"ftp scheme", `curl 'ftp://api.partner.com/x'`, ErrInvalidURL},
		{"gopher scheme", `curl 'gopher://api.partner.com/x'`, ErrInvalidURL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.command)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.command, err, tc.wantErr)
			}
		})
	}
}

func TestRedacted(t *testing.T) {
	req, err := Parse(`curl 'https://api.partner.com/x' ` +
		`-H 'Authorization: Bearer secret-token-123' ` +
		`-H 'Accept: application/json' ` +
		`-u 'alice:hunter2'`)
	if err != nil {
		t.Fatalf("Parse() = %v", err)
	}

	safe := req.Redacted()

	for _, h := range safe.Headers {
		switch h.Name {
		case "Authorization":
			if h.Value != Mask {
				t.Errorf("Authorization = %q, want it masked", h.Value)
			}
		case "Accept":
			if h.Value != "application/json" {
				t.Errorf("Accept = %q, want it left alone", h.Value)
			}
		}
	}

	if safe.Auth == nil {
		t.Fatal("Auth = nil, want the auth block preserved")
	}
	if safe.Auth.Password != Mask {
		t.Errorf("Auth.Password = %q, want it masked", safe.Auth.Password)
	}
	// The username is not a credential on its own and the admin needs to
	// see it to confirm they imported the right command.
	if safe.Auth.Username != "alice" {
		t.Errorf("Auth.Username = %q, want alice", safe.Auth.Username)
	}

	// Redaction must not damage the caller's copy: the plaintext values
	// still have to reach the secret store after this call.
	if req.Auth.Password != "hunter2" {
		t.Errorf("receiver mutated: Auth.Password = %q, want hunter2", req.Auth.Password)
	}
	for _, h := range req.Headers {
		if h.Name == "Authorization" && h.Value != "Bearer secret-token-123" {
			t.Errorf("receiver mutated: Authorization = %q", h.Value)
		}
	}
}

func TestPathSegments(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		{"/products/P123", 2},
		{"/products/P123/variants/V9", 4},
		{"/", 0},
		{"", 0},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := ImportedRequest{Path: tc.path}
			if got := req.PathSegments(); len(got) != tc.want {
				t.Fatalf("PathSegments(%q) = %#v, want %d segments", tc.path, got, tc.want)
			}
		})
	}
}

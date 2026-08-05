package curlimport

import (
	"errors"
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "plain words",
			input: `curl https://api.partner.com/products`,
			want:  []string{"curl", "https://api.partner.com/products"},
		},
		{
			name:  "single quotes keep spaces together",
			input: `curl 'Accept: application/json'`,
			want:  []string{"curl", "Accept: application/json"},
		},
		{
			name:  "double quotes keep spaces together",
			input: `curl "Accept: application/json"`,
			want:  []string{"curl", "Accept: application/json"},
		},
		{
			name:  "escaped quote inside double quotes",
			input: `curl "a\"b"`,
			want:  []string{"curl", `a"b`},
		},
		{
			name:  "escaped backslash inside double quotes",
			input: `curl "a\\b"`,
			want:  []string{"curl", `a\b`},
		},
		{
			// Single quotes are literal in a shell, so a dollar sign
			// inside them cannot expand and is safe to keep.
			name:  "dollar sign inside single quotes is literal",
			input: `curl 'a$b'`,
			want:  []string{"curl", `a$b`},
		},
		{
			name:  "backtick inside single quotes is literal",
			input: "curl 'a`b'",
			want:  []string{"curl", "a`b"},
		},
		{
			name:  "backslash escape outside quotes",
			input: `curl a\ b`,
			want:  []string{"curl", "a b"},
		},
		{
			name:  "line continuation joins the command",
			input: "curl \\\n  https://api.partner.com",
			want:  []string{"curl", "https://api.partner.com"},
		},
		{
			name:  "tabs and newlines separate tokens",
			input: "curl\thttps://api.partner.com\n-X\tGET",
			want:  []string{"curl", "https://api.partner.com", "-X", "GET"},
		},
		{
			name:  "empty quotes produce an empty token",
			input: `curl ''`,
			want:  []string{"curl", ""},
		},
		{
			name:  "adjacent quoted and bare text is one token",
			input: `curl 'https://x/'products`,
			want:  []string{"curl", "https://x/products"},
		},
		{
			name:  "empty input yields no tokens",
			input: ``,
			want:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Tokenize(tc.input)
			if err != nil {
				t.Fatalf("Tokenize(%q) = %v, want nil error", tc.input, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Tokenize(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

// The importer's central promise is that the cURL string is parsed, never
// executed. Shell metacharacters have no meaning to a parser, so their
// presence means the input is not the single HTTP request it claims to
// be — refusing beats silently reinterpreting it.
func TestTokenizeRejectsShellConstructs(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"pipe", `curl https://x | tee out`},
		{"semicolon", `curl https://x ; rm -rf /`},
		{"ampersand", `curl https://x & whoami`},
		{"output redirect", `curl https://x > /tmp/out`},
		{"input redirect", `curl https://x < /etc/passwd`},
		{"unquoted dollar", `curl https://x/$HOME`},
		{"unquoted backtick", "curl https://x/`whoami`"},
		{"dollar inside double quotes", `curl "https://x/$HOME"`},
		{"backtick inside double quotes", "curl \"https://x/`whoami`\""},
		{"command substitution", `curl "$(cat /etc/passwd)"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Tokenize(tc.input); !errors.Is(err, ErrDangerousToken) {
				t.Fatalf("Tokenize(%q) = %v, want %v", tc.input, err, ErrDangerousToken)
			}
		})
	}
}

func TestTokenizeMalformedQuoting(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"unterminated single quote", `curl 'abc`, ErrUnterminatedQuote},
		{"unterminated double quote", `curl "abc`, ErrUnterminatedQuote},
		{"trailing backslash", `curl abc\`, ErrTrailingBackslash},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Tokenize(tc.input); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Tokenize(%q) = %v, want %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

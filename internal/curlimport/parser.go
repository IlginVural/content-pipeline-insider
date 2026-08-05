package curlimport

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// urlEncodeDataPart applies curl's --data-urlencode semantics: for
// "name=value" the name is left alone and only the value is encoded; a
// bare "value" or "=value" is encoded whole.
//
// The result is stored already encoded, which both downstream paths
// handle correctly — as a body it is the exact wire format, and with -G
// it round-trips through url.ParseQuery back to the intended value.
func urlEncodeDataPart(val string) (string, error) {
	if name, value, found := strings.Cut(val, "="); found && name != "" {
		if strings.ContainsAny(name, "&") {
			return "", fmt.Errorf("%w: %q", ErrInvalidDataField, name)
		}
		return name + "=" + url.QueryEscape(value), nil
	}
	return url.QueryEscape(strings.TrimPrefix(val, "=")), nil
}

// Parse tokenizes and parses a full cURL command into an ImportedRequest.
// It never invokes a shell or touches the filesystem — every value comes
// from the token stream alone.
func Parse(command string) (*ImportedRequest, error) {
	tokens, err := Tokenize(command)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, ErrEmptyCommand
	}
	if tokens[0] != "curl" {
		return nil, ErrNotCurl
	}

	req := &ImportedRequest{}
	var rawURL string
	var isGet bool
	var dataParts []string

	for i := 1; i < len(tokens); {
		tok := tokens[i]

		if strings.HasPrefix(tok, "-") {
			allowed, denied, takesValue := classifyFlag(tok)
			if denied {
				return nil, fmt.Errorf("%w: %s", ErrDangerousFlag, tok)
			}
			if !allowed {
				return nil, fmt.Errorf("%w: %s", ErrUnsupportedFlag, tok)
			}
			if !takesValue {
				if tok == "-G" || tok == "--get" {
					isGet = true
				}
				i++
				continue
			}
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("%w: %s", ErrMissingFlagValue, tok)
			}
			val := tokens[i+1]
			i += 2

			switch tok {
			case "-X", "--request":
				req.Method = strings.ToUpper(val)
			case "-H", "--header":
				name, value, ok := strings.Cut(val, ":")
				if !ok {
					return nil, fmt.Errorf("%w: %q", ErrInvalidHeader, val)
				}
				name, value = strings.TrimSpace(name), strings.TrimSpace(value)
				req.Headers = append(req.Headers, ImportedHeader{
					Name:        name,
					Value:       value,
					IsSensitive: isSensitiveHeader(name),
				})
			case "-u", "--user":
				user, pass, ok := strings.Cut(val, ":")
				if !ok {
					return nil, fmt.Errorf("%w: %q", ErrInvalidUser, val)
				}
				req.Auth = &ImportedAuth{Username: user, Password: pass}
			case "--url":
				rawURL = val
			case "-d", "--data", "--data-raw":
				if strings.HasPrefix(val, "@") {
					return nil, fmt.Errorf("%w: reading data from a file is not allowed (%s %s)", ErrDangerousFlag, tok, val)
				}
				dataParts = append(dataParts, val)
			case "--data-urlencode":
				if strings.HasPrefix(val, "@") {
					return nil, fmt.Errorf("%w: reading data from a file is not allowed (%s %s)", ErrDangerousFlag, tok, val)
				}
				// Unlike -d, this flag percent-encodes its content. Storing
				// it raw produced a request that differed from the one the
				// administrator tested, with no indication anything changed.
				encoded, err := urlEncodeDataPart(val)
				if err != nil {
					return nil, err
				}
				dataParts = append(dataParts, encoded)
			}
			continue
		}

		// A bare token is the positional URL — curl allows it anywhere
		// in the argument list, not just at a fixed position.
		if rawURL == "" {
			rawURL = tok
		}
		i++
	}

	if rawURL == "" {
		return nil, ErrMissingURL
	}
	if strings.Contains(strings.ToLower(rawURL), "file://") {
		return nil, fmt.Errorf("%w: file:// URLs are not allowed", ErrDangerousFlag)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidURL, u.Scheme)
	}

	// Credentials in the URL are real credentials. u.Host excludes the
	// userinfo section, so building BaseURL from it silently dropped
	// them: an administrator pasted a working command and got a
	// configuration with no authentication, which then failed against
	// the partner for no visible reason. An explicit -u wins, matching
	// curl's own precedence.
	if u.User != nil && req.Auth == nil {
		password, _ := u.User.Password()
		req.Auth = &ImportedAuth{Username: u.User.Username(), Password: password}
	}

	req.BaseURL = u.Scheme + "://" + u.Host
	req.Path = u.Path

	query := u.Query()
	switch {
	case isGet && len(dataParts) > 0:

		for _, part := range dataParts {
			extra, err := url.ParseQuery(part)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
			}
			for k, vs := range extra {
				for _, v := range vs {
					query.Add(k, v)
				}
			}
		}
	case len(dataParts) > 0:
		req.Body = strings.Join(dataParts, "&")
		if req.Method == "" {
			req.Method = "POST"
		}
	}

	for name, values := range query {
		for _, v := range values {
			req.Query = append(req.Query, ImportedParameter{
				Name:        name,
				Value:       v,
				IsSensitive: isSensitiveQueryParam(name),
			})
		}
	}
	sort.Slice(req.Query, func(i, j int) bool {
		if req.Query[i].Name != req.Query[j].Name {
			return req.Query[i].Name < req.Query[j].Name
		}
		return req.Query[i].Value < req.Query[j].Value
	})

	if req.Method == "" {
		req.Method = "GET"
	}
	return req, nil
}


package curlimport

import (
	"fmt"
	"strings"
)


func Tokenize(input string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	hasCur := false

	runes := []rune(input)
	n := len(runes)

	const inNormal, inSingle, inDouble = 0, 1, 2
	state := inNormal

	flush := func() {
		if hasCur {
			tokens = append(tokens, cur.String())
			cur.Reset()
			hasCur = false
		}
	}

	for i := 0; i < n; {
		c := runes[i]

		switch state {
		case inSingle:
			
			if c == '\'' {
				state = inNormal
				i++
				continue
			}
			cur.WriteRune(c)
			hasCur = true
			i++

		case inDouble:
			if c == '"' {
				state = inNormal
				i++
				continue
			}
			if c == '\\' && i+1 < n && isDoubleQuoteEscapable(runes[i+1]) {
				cur.WriteRune(runes[i+1])
				hasCur = true
				i += 2
				continue
			}
		
			if c == '$' || c == '`' {
				return nil, fmt.Errorf("%w: %q inside double quotes", ErrDangerousToken, string(c))
			}
			cur.WriteRune(c)
			hasCur = true
			i++

		default: // inNormal
			switch {
			case c == '\'':
				state = inSingle
				hasCur = true
				i++
			case c == '"':
				state = inDouble
				hasCur = true
				i++
			case c == '\\':
				if i+1 < n && runes[i+1] == '\n' {
					// Line continuation: drop both characters, don't
					// end the current token.
					i += 2
					continue
				}
				if i+1 >= n {
					return nil, ErrTrailingBackslash
				}
				cur.WriteRune(runes[i+1])
				hasCur = true
				i += 2
			case c == ' ' || c == '\t' || c == '\n' || c == '\r':
				flush()
				i++
			case strings.ContainsRune("|;&<>$`", c):
				return nil, fmt.Errorf("%w: unquoted %q", ErrDangerousToken, string(c))
			default:
				cur.WriteRune(c)
				hasCur = true
				i++
			}
		}
	}

	if state != inNormal {
		return nil, ErrUnterminatedQuote
	}
	flush()
	return tokens, nil
}

func isDoubleQuoteEscapable(c rune) bool {
	switch c {
	case '"', '\\', '$', '`':
		return true
	default:
		return false
	}
}
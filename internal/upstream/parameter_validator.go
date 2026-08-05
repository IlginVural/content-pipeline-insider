package upstream

import (
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"unicode/utf8"
)

// patternCache memoizes compiled validation patterns.
//
// Parameters are validated on every resolve, which happens once per
// message rendered — recompiling an administrator's regular expression
// in that path is work repeated millions of times per send for a result
// that never changes. Patterns come from stored configuration, so the
// set is bounded by the number of published integrations.
//
// Compilation failures are cached too: a bad pattern is equally invalid
// on the second call, and retrying it per render would be the same waste.
var patternCache sync.Map // pattern string -> *regexp.Regexp | error

func compilePattern(pattern string) (*regexp.Regexp, error) {
	if cached, ok := patternCache.Load(pattern); ok {
		switch v := cached.(type) {
		case *regexp.Regexp:
			return v, nil
		case error:
			return nil, v
		}
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		patternCache.Store(pattern, err)
		return nil, err
	}
	patternCache.Store(pattern, re)
	return re, nil
}

func ResolveParameters(params map[string]ParameterDef, values map[string]string) (map[string]string, error) {
	resolved := make(map[string]string, len(params))

	for name, def := range params {
		val, provided := values[name]
		if !provided {
			if def.Required {
				return nil, fmt.Errorf("%w: %s", ErrMissingParameter, name)
			}
			if def.Default == "" {
				continue
			}
			val = def.Default
		}
		if err := validateValue(name, def, val); err != nil {
			return nil, err
		}
		resolved[name] = val
	}

	for name := range values {
		if _, known := params[name]; !known {
			return nil, fmt.Errorf("%w: %s", ErrUnknownParameter, name)
		}
	}
	return resolved, nil
}

func validateValue(name string, def ParameterDef, val string) error {
	if err := validateType(name, def.Type, val); err != nil {
		return err
	}
	if v := def.Validation; v != nil {
		// Characters, not bytes. Counting bytes made the effective limit
		// depend on the alphabet — a 40-character Turkish or Japanese
		// value would be refused by a maximumLength of 100 that an
		// ASCII value of the same length passed.
		if v.MaximumLength > 0 && utf8.RuneCountInString(val) > v.MaximumLength {
			return fmt.Errorf("%w: %s exceeds maximum length %d", ErrInvalidParameter, name, v.MaximumLength)
		}
		if v.Pattern != "" {
			re, err := compilePattern(v.Pattern)
			if err != nil {
				return fmt.Errorf("%w: %s has an invalid pattern: %v", ErrInvalidParameter, name, err)
			}
			if !re.MatchString(val) {
				return fmt.Errorf("%w: %s does not match required pattern", ErrInvalidParameter, name)
			}
		}
	}
	if len(def.AllowedValues) > 0 && !contains(def.AllowedValues, val) {
		return fmt.Errorf("%w: %s is not one of the allowed values", ErrInvalidParameter, name)
	}
	return nil
}

func validateType(name, typ, val string) error {
	switch typ {
	case "", "string":
		return nil
	case "integer":
		if _, err := strconv.ParseInt(val, 10, 64); err != nil {
			return fmt.Errorf("%w: %s must be an integer", ErrInvalidParameter, name)
		}
	case "number":
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			return fmt.Errorf("%w: %s must be a number", ErrInvalidParameter, name)
		}
	case "boolean":
		if _, err := strconv.ParseBool(val); err != nil {
			return fmt.Errorf("%w: %s must be a boolean", ErrInvalidParameter, name)
		}
	default:
		return fmt.Errorf("%w: %s has unknown type %q", ErrInvalidParameter, name, typ)
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

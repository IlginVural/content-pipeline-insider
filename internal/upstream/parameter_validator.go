package upstream

import (
	"fmt"
	"regexp"
)

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
	if def.MaximumLength > 0 && len(val) > def.MaximumLength {
		return fmt.Errorf("%w: %s exceeds maximum length %d", ErrInvalidParameter, name, def.MaximumLength)
	}
	if def.Pattern != "" {
		re, err := regexp.Compile(def.Pattern)
		if err != nil {
			return fmt.Errorf("%w: %s has an invalid pattern: %v", ErrInvalidParameter, name, err)
		}
		if !re.MatchString(val) {
			return fmt.Errorf("%w: %s does not match required pattern", ErrInvalidParameter, name)
		}
	}
	if len(def.AllowedValues) > 0 && !contains(def.AllowedValues, val) {
		return fmt.Errorf("%w: %s is not one of the allowed values", ErrInvalidParameter, name)
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

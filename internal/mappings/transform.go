package mappings

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/jmespath-community/go-jmespath"
)

// Apply builds the normalized object from a live partner response.
//
// It constructs a completely new map rather than filtering the response in
// place, so unselected fields — a supplier's email address, an internal cost
// — are discarded rather than travelling downstream with the parts that were
// chosen.
//
// The response must come from responseparser.Decode, which decodes with
// UseNumber. That is what makes integer coercion exact: a large product ID
// survives instead of being rounded through float64. jmespath navigates maps
// and slices without converting the leaves, so json.Number arrives intact.
func Apply(set []FieldMapping, response any) (map[string]any, error) {
	out := make(map[string]any, len(set))

	for _, m := range set {
		raw, err := jmespath.Search(m.SourcePath, response)
		if err != nil {
			return nil, fmt.Errorf("mappings: evaluate %s for %q: %w", m.SourcePath, m.OutputName, err)
		}

		if raw == nil {
			// The field is absent, or present and null. Both mean "no value",
			// and the mapping says what to do about it.
			switch {
			case m.DefaultValue != nil:
				v, err := decodeDefault(m)
				if err != nil {
					return nil, err
				}
				out[m.OutputName] = v
			case m.Required:
				return nil, fmt.Errorf("%w: %q (%s)", ErrRequiredFieldMissing, m.OutputName, m.SourcePath)
			default:
				// Omit the key entirely. The renderer hides the block rather
				// than printing an empty one.
			}
			continue
		}

		v, ok := coerce(m.DataType, raw)
		if !ok {
			// This is the check that makes data_type load-bearing rather than
			// decorative. Without it, 121 and "121" are indistinguishable and
			// a partner that quietly changes a type breaks emails at send
			// time instead of failing here.
			return nil, fmt.Errorf("%w: %q declares %s but %s returned %s",
				ErrWrongRuntimeType, m.OutputName, m.DataType, m.SourcePath, describe(raw))
		}
		out[m.OutputName] = v
	}

	return out, nil
}

// coerce turns a decoded JSON value into the Go type the mapping declared,
// reporting whether the value was acceptable at all.
func coerce(t DataType, raw any) (any, bool) {
	switch t {
	case TypeString:
		s, ok := raw.(string)
		return s, ok

	case TypeInteger:
		n, ok := raw.(json.Number)
		if !ok {
			return nil, false
		}
		// Int64 fails on a fractional or out-of-range value, which is a
		// genuine mismatch with a field declared integer.
		i, err := n.Int64()
		if err != nil {
			return nil, false
		}
		return i, true

	case TypeNumber:
		n, ok := raw.(json.Number)
		if !ok {
			return nil, false
		}
		f, err := n.Float64()
		if err != nil {
			return nil, false
		}
		return f, true

	case TypeBoolean:
		b, ok := raw.(bool)
		return b, ok
	}
	return nil, false
}

// decodeDefault reads the stored default the same way responseparser reads a
// partner response, so a default and a live value of the same field arrive as
// the same Go type and the renderer cannot tell them apart.
func decodeDefault(m FieldMapping) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(m.DefaultValue))
	dec.UseNumber()

	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrBadDefault, m.OutputName, err)
	}

	v, ok := coerce(m.DataType, raw)
	if !ok {
		return nil, fmt.Errorf("%w: %q declares %s but the default is %s",
			ErrBadDefault, m.OutputName, m.DataType, describe(raw))
	}
	return v, nil
}

// describe names a decoded value's JSON type for error messages. "expected
// integer, got a string" is actionable; "expected integer" alone is not.
func describe(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case json.Number:
		return "the number " + t.String()
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	default:
		return fmt.Sprintf("%T", v)
	}
}

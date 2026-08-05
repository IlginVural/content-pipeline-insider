// Package mappings is the transformation stage of the pipeline: the set of
// fields an administrator selected out of a discovered response, plus the
// rules for turning a live partner response into the normalized object an
// email template renders.
//
// Only the selection is persisted. The discovered field tree that produced it
// is built in memory by schemainfer, shown to the administrator, and then
// discarded — the mappings here already carry everything the runtime needs.
package mappings

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DataType mirrors the field_mappings_data_type_check constraint.
//
// It is deliberately narrower than schemainfer.Type. Objects and arrays are
// not selectable, and a field observed only as null has no known type, so
// neither can reach the table.
type DataType string

const (
	TypeString  DataType = "string"
	TypeInteger DataType = "integer"
	TypeNumber  DataType = "number"
	TypeBoolean DataType = "boolean"
)

func (t DataType) Valid() bool {
	switch t {
	case TypeString, TypeInteger, TypeNumber, TypeBoolean:
		return true
	}
	return false
}

// FieldMapping is one row of content_pipeline_field_mappings: one field the
// administrator chose to expose, and everything needed to extract, type, and
// display it.
type FieldMapping struct {
	// OutputName is what the email template writes. It is also the key in the
	// normalized object Apply produces.
	OutputName string `json:"outputName"`

	// SourcePath is the JMESPath to evaluate against the live response. It
	// comes from schemainfer.SchemaNode.JMESPath for the node the admin
	// picked, so it may be a projection or contain quoted keys.
	SourcePath string `json:"sourcePath"`

	DataType     DataType `json:"dataType"`
	DisplayLabel string   `json:"displayLabel,omitempty"`
	Required     bool     `json:"required"`

	// DefaultValue is the raw JSON the admin supplied, or nil when there is
	// no default. Kept as json.RawMessage rather than a decoded any so the
	// value stays exactly as entered until coerce checks it against DataType.
	//
	// A JSON null is not a usable default: it is neither a string, a number,
	// nor a boolean, so coerce rejects it and so does the
	// field_mappings_default_type_check constraint. nil is therefore the only
	// way to express "no default", and there is no second representation to
	// tell it apart from.
	DefaultValue json.RawMessage `json:"defaultValue,omitempty"`

	SortOrder int `json:"sortOrder"`
}

const (
	maxOutputNameLen   = 128
	maxDisplayLabelLen = 255
)

// outputNamePattern is the same expression as the SQL CHECK constraint.
// Duplicated on purpose: the database is the last line of defence, but a bad
// request should fail with a readable message long before it gets there.
var outputNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate enforces the same rules as the table constraints.
func (m FieldMapping) Validate() error {
	if !outputNamePattern.MatchString(m.OutputName) {
		return fmt.Errorf("%w: %q", ErrInvalidOutputName, m.OutputName)
	}

	// Bytes, not runes, is correct here and only here: the pattern above
	// admits nothing outside ASCII, so the two counts are identical.
	if len(m.OutputName) > maxOutputNameLen {
		return fmt.Errorf("%w: %q is %d characters, limit is %d",
			ErrOutputNameTooLong, m.OutputName, len(m.OutputName), maxOutputNameLen)
	}

	if strings.TrimSpace(m.SourcePath) == "" {
		return fmt.Errorf("%w: %q", ErrEmptySourcePath, m.OutputName)
	}

	if !m.DataType.Valid() {
		return fmt.Errorf("%w: %q declares %q", ErrInvalidDataType, m.OutputName, m.DataType)
	}

	// VARCHAR(255) counts characters, so this has to as well. Counting bytes
	// would reject a Turkish label at roughly half the length Postgres
	// accepts, since ı, ş, ğ, ü, ö, and ç are two bytes each.
	if utf8.RuneCountInString(m.DisplayLabel) > maxDisplayLabelLen {
		return fmt.Errorf("%w: %q is %d characters, limit is %d",
			ErrDisplayLabelTooLong, m.OutputName,
			utf8.RuneCountInString(m.DisplayLabel), maxDisplayLabelLen)
	}

	// Mirrors field_mappings_sort_order_check. A negative position is always
	// a client bug rather than an intent, and catching it here names the
	// field instead of surfacing a constraint violation.
	if m.SortOrder < 0 {
		return fmt.Errorf("%w: %q has %d", ErrNegativeSortOrder, m.OutputName, m.SortOrder)
	}

	// required says "fail or fall back when this is missing"; a default says
	// it can never be missing. Both together is an unexpressed intent.
	if m.Required && m.DefaultValue != nil {
		return fmt.Errorf("%w: %q", ErrRequiredWithDefault, m.OutputName)
	}

	// Decoding the default here catches a string field defaulting to false
	// before Postgres does, and with a message that names the field.
	if m.DefaultValue != nil {
		if _, err := decodeDefault(m); err != nil {
			return err
		}
	}

	return nil
}

// ValidateSet checks a whole selection, including that no two fields claim
// the same output name — a collision the primary key would otherwise reject
// as an opaque constraint violation.
//
// Two fields sharing a sort_order is deliberately allowed: the
// "ORDER BY sort_order, output_name" tiebreak makes the result deterministic,
// and rejecting ties would make reordering in the UI needlessly fragile.
func ValidateSet(set []FieldMapping) error {
	if len(set) == 0 {
		return ErrEmptySet
	}
	seen := make(map[string]struct{}, len(set))
	for _, m := range set {
		if err := m.Validate(); err != nil {
			return err
		}
		if _, dup := seen[m.OutputName]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateOutputName, m.OutputName)
		}
		seen[m.OutputName] = struct{}{}
	}
	return nil
}

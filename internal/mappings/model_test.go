package mappings_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"content-pipeline-insider/internal/mappings"
)

// valid is the baseline every case below varies one field of, so each test
// name states exactly what makes that case invalid.
func valid() mappings.FieldMapping {
	return mappings.FieldMapping{
		OutputName:   "productName",
		SourcePath:   "product.name",
		DataType:     mappings.TypeString,
		DisplayLabel: "Product name",
		SortOrder:    0,
	}
}

func TestFieldMappingValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*mappings.FieldMapping)
		want   error // nil means the mapping is expected to be accepted
	}{
		{name: "baseline", mutate: func(*mappings.FieldMapping) {}},
		{
			name:   "output name with a hyphen",
			mutate: func(m *mappings.FieldMapping) { m.OutputName = "product-name" },
			want:   mappings.ErrInvalidOutputName,
		},
		{
			name:   "output name starting with a digit",
			mutate: func(m *mappings.FieldMapping) { m.OutputName = "1stChoice" },
			want:   mappings.ErrInvalidOutputName,
		},
		{
			name:   "empty output name",
			mutate: func(m *mappings.FieldMapping) { m.OutputName = "" },
			want:   mappings.ErrInvalidOutputName,
		},
		{
			name:   "non-ASCII output name",
			mutate: func(m *mappings.FieldMapping) { m.OutputName = "ürünAdı" },
			want:   mappings.ErrInvalidOutputName,
		},
		{
			name:   "leading underscore is allowed",
			mutate: func(m *mappings.FieldMapping) { m.OutputName = "_internal" },
		},
		{
			name:   "output name at the limit",
			mutate: func(m *mappings.FieldMapping) { m.OutputName = "a" + strings.Repeat("b", 127) },
		},
		{
			name:   "output name over the limit",
			mutate: func(m *mappings.FieldMapping) { m.OutputName = "a" + strings.Repeat("b", 128) },
			want:   mappings.ErrOutputNameTooLong,
		},
		{
			name:   "empty source path",
			mutate: func(m *mappings.FieldMapping) { m.SourcePath = "" },
			want:   mappings.ErrEmptySourcePath,
		},
		{
			name:   "whitespace-only source path",
			mutate: func(m *mappings.FieldMapping) { m.SourcePath = "   " },
			want:   mappings.ErrEmptySourcePath,
		},
		{
			name:   "unknown data type",
			mutate: func(m *mappings.FieldMapping) { m.DataType = "date" },
			want:   mappings.ErrInvalidDataType,
		},
		{
			name:   "object is not a selectable data type",
			mutate: func(m *mappings.FieldMapping) { m.DataType = "object" },
			want:   mappings.ErrInvalidDataType,
		},
		{
			name:   "empty data type",
			mutate: func(m *mappings.FieldMapping) { m.DataType = "" },
			want:   mappings.ErrInvalidDataType,
		},
		{
			// 255 two-byte runes are 510 bytes. Counting bytes here would
			// reject a label Postgres accepts, so this case is the difference
			// between the two counts.
			name:   "multi-byte display label at the character limit",
			mutate: func(m *mappings.FieldMapping) { m.DisplayLabel = strings.Repeat("ş", 255) },
		},
		{
			name:   "multi-byte display label over the character limit",
			mutate: func(m *mappings.FieldMapping) { m.DisplayLabel = strings.Repeat("ş", 256) },
			want:   mappings.ErrDisplayLabelTooLong,
		},
		{
			name:   "negative sort order",
			mutate: func(m *mappings.FieldMapping) { m.SortOrder = -1 },
			want:   mappings.ErrNegativeSortOrder,
		},
		{
			name: "required and default together",
			mutate: func(m *mappings.FieldMapping) {
				m.Required = true
				m.DefaultValue = json.RawMessage(`"unspecified"`)
			},
			want: mappings.ErrRequiredWithDefault,
		},
		{
			name: "default of the wrong type",
			mutate: func(m *mappings.FieldMapping) {
				m.DataType = mappings.TypeString
				m.DefaultValue = json.RawMessage(`false`)
			},
			want: mappings.ErrBadDefault,
		},
		{
			name: "fractional default on an integer field",
			mutate: func(m *mappings.FieldMapping) {
				m.DataType = mappings.TypeInteger
				m.DefaultValue = json.RawMessage(`1.5`)
			},
			want: mappings.ErrBadDefault,
		},
		{
			// jsonb_typeof returns 'null' for a JSON null, which every branch
			// of field_mappings_default_type_check rejects. nil is the only
			// way to say "no default", so this must fail here too.
			name: "explicit JSON null default",
			mutate: func(m *mappings.FieldMapping) {
				m.DefaultValue = json.RawMessage(`null`)
			},
			want: mappings.ErrBadDefault,
		},
		{
			name: "integer default on a number field",
			mutate: func(m *mappings.FieldMapping) {
				m.DataType = mappings.TypeNumber
				m.DefaultValue = json.RawMessage(`0`)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			tc.mutate(&m)

			err := m.Validate()
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDataTypeValid(t *testing.T) {
	for _, ok := range []mappings.DataType{
		mappings.TypeString, mappings.TypeInteger, mappings.TypeNumber, mappings.TypeBoolean,
	} {
		if !ok.Valid() {
			t.Errorf("%q.Valid() = false, want true", ok)
		}
	}
	// The types the migration's data_type CHECK deliberately excludes.
	for _, bad := range []mappings.DataType{"", "null", "object", "array", "mixed", "String"} {
		if bad.Valid() {
			t.Errorf("%q.Valid() = true, want false", bad)
		}
	}
}

func TestValidateSet(t *testing.T) {
	t.Run("empty set", func(t *testing.T) {
		if err := mappings.ValidateSet(nil); !errors.Is(err, mappings.ErrEmptySet) {
			t.Fatalf("ValidateSet(nil) = %v, want %v", err, mappings.ErrEmptySet)
		}
		if err := mappings.ValidateSet([]mappings.FieldMapping{}); !errors.Is(err, mappings.ErrEmptySet) {
			t.Fatalf("ValidateSet(empty) = %v, want %v", err, mappings.ErrEmptySet)
		}
	})

	t.Run("duplicate output names", func(t *testing.T) {
		first := valid()
		second := valid()
		second.SourcePath = "product.title" // different source, same output name

		err := mappings.ValidateSet([]mappings.FieldMapping{first, second})
		if !errors.Is(err, mappings.ErrDuplicateOutputName) {
			t.Fatalf("ValidateSet = %v, want %v", err, mappings.ErrDuplicateOutputName)
		}
	})

	t.Run("shared sort order is allowed", func(t *testing.T) {
		// The ORDER BY sort_order, output_name tiebreak makes ties
		// deterministic, so rejecting them would only make reordering in the
		// UI fragile for no gain.
		first := valid()
		second := valid()
		second.OutputName = "productTitle"
		second.SortOrder = first.SortOrder

		if err := mappings.ValidateSet([]mappings.FieldMapping{first, second}); err != nil {
			t.Fatalf("ValidateSet: unexpected error: %v", err)
		}
	})

	t.Run("a bad member fails the whole set", func(t *testing.T) {
		good := valid()
		bad := valid()
		bad.OutputName = "product-price"

		err := mappings.ValidateSet([]mappings.FieldMapping{good, bad})
		if !errors.Is(err, mappings.ErrInvalidOutputName) {
			t.Fatalf("ValidateSet = %v, want %v", err, mappings.ErrInvalidOutputName)
		}
	})
}

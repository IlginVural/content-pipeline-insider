package mappings_test

import (
	"errors"
	"testing"

	"content-pipeline-insider/internal/mappings"
	"content-pipeline-insider/internal/responseparser"
	"content-pipeline-insider/internal/schemainfer"
)

// infer builds the discovered tree the way the configuration flow does: decode
// the sample responses, then describe their merged shape.
func infer(t *testing.T, bodies ...string) *schemainfer.SchemaNode {
	t.Helper()

	samples := make([]any, 0, len(bodies))
	for _, body := range bodies {
		v, err := responseparser.Decode([]byte(body))
		if err != nil {
			t.Fatalf("decode sample: %v", err)
		}
		samples = append(samples, v)
	}

	tree, err := schemainfer.Infer(samples)
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	return tree
}

const schemaBody = `{
	"product": {
		"name": "Kettle",
		"stock": 12,
		"price": 249.90,
		"inStock": true,
		"discount": null
	},
	"order-id": "A-71"
}`

func TestValidateAgainstSchema(t *testing.T) {
	tests := []struct {
		name string
		set  []mappings.FieldMapping
		want error
	}{
		{
			name: "every declared type matches what was discovered",
			set: []mappings.FieldMapping{
				{OutputName: "productName", SourcePath: "product.name", DataType: mappings.TypeString},
				{OutputName: "stock", SourcePath: "product.stock", DataType: mappings.TypeInteger},
				{OutputName: "price", SourcePath: "product.price", DataType: mappings.TypeNumber},
				{OutputName: "inStock", SourcePath: "product.inStock", DataType: mappings.TypeBoolean},
			},
		},
		{
			// Widening is always safe: every integer is a valid number.
			name: "integer declared as number",
			set: []mappings.FieldMapping{
				{OutputName: "stock", SourcePath: "product.stock", DataType: mappings.TypeNumber},
			},
		},
		{
			// The reverse is not: the first fractional price would fail at
			// render time, long after the mapping was saved.
			name: "number declared as integer",
			set: []mappings.FieldMapping{
				{OutputName: "price", SourcePath: "product.price", DataType: mappings.TypeInteger},
			},
			want: mappings.ErrTypeMismatch,
		},
		{
			name: "string declared as boolean",
			set: []mappings.FieldMapping{
				{OutputName: "productName", SourcePath: "product.name", DataType: mappings.TypeBoolean},
			},
			want: mappings.ErrTypeMismatch,
		},
		{
			name: "path that was never in the response",
			set: []mappings.FieldMapping{
				{OutputName: "colour", SourcePath: "product.colour", DataType: mappings.TypeString},
			},
			want: mappings.ErrUnknownSourcePath,
		},
		{
			// Selecting a container would drag every unselected field inside
			// it along, which is the one thing the transformation stage exists
			// to prevent. Flatten never offers one, so it cannot be selected.
			name: "object container",
			set: []mappings.FieldMapping{
				{OutputName: "product", SourcePath: "product", DataType: mappings.TypeString},
			},
			want: mappings.ErrUnknownSourcePath,
		},
		{
			name: "field that was null in every sample",
			set: []mappings.FieldMapping{
				{OutputName: "discount", SourcePath: "product.discount", DataType: mappings.TypeNumber},
			},
			want: mappings.ErrUntypedField,
		},
		{
			name: "quoted key",
			set: []mappings.FieldMapping{
				{OutputName: "orderID", SourcePath: `"order-id"`, DataType: mappings.TypeString},
			},
		},
		{
			// The unquoted form is a different expression — JMESPath reads it
			// as subtraction — so it is not in the tree and must not validate.
			name: "unquoted form of a key that needs quoting",
			set: []mappings.FieldMapping{
				{OutputName: "orderID", SourcePath: "order-id", DataType: mappings.TypeString},
			},
			want: mappings.ErrUnknownSourcePath,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := mappings.ValidateAgainstSchema(infer(t, schemaBody), tc.set)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("ValidateAgainstSchema: unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateAgainstSchema = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestValidateAgainstSchemaNilTree covers the case where no sample was ever
// fetched, which is a configuration error rather than a mapping error.
func TestValidateAgainstSchemaNilTree(t *testing.T) {
	set := []mappings.FieldMapping{
		{OutputName: "productName", SourcePath: "product.name", DataType: mappings.TypeString},
	}
	if err := mappings.ValidateAgainstSchema(nil, set); !errors.Is(err, schemainfer.ErrNoSamples) {
		t.Fatalf("ValidateAgainstSchema(nil) = %v, want %v", err, schemainfer.ErrNoSamples)
	}
}

// TestValidateAgainstSchemaMultipleSamples covers the reason merging exists: a
// field null in the first sample has a known type once a second sample carries
// a value, and a field that changes type across samples becomes unselectable.
func TestValidateAgainstSchemaMultipleSamples(t *testing.T) {
	tree := infer(t,
		`{"product":{"discount":null,"stock":12}}`,
		`{"product":{"discount":15.5,"stock":8}}`,
	)

	t.Run("null in one sample, typed in another", func(t *testing.T) {
		err := mappings.ValidateAgainstSchema(tree, []mappings.FieldMapping{
			{OutputName: "discount", SourcePath: "product.discount", DataType: mappings.TypeNumber},
		})
		if err != nil {
			t.Fatalf("ValidateAgainstSchema: unexpected error: %v", err)
		}
	})

	t.Run("still rejects a wrong declared type", func(t *testing.T) {
		err := mappings.ValidateAgainstSchema(tree, []mappings.FieldMapping{
			{OutputName: "discount", SourcePath: "product.discount", DataType: mappings.TypeString},
		})
		if !errors.Is(err, mappings.ErrTypeMismatch) {
			t.Fatalf("ValidateAgainstSchema = %v, want %v", err, mappings.ErrTypeMismatch)
		}
	})
}

// TestArrayProjectionRejectedAtSaveTime is the regression test for a gap the
// two stages used to disagree about.
//
// "reviews[].rating" is a projection: jmespath returns every rating, and no
// DataType describes a list, so Apply always rejected it. schemainfer used to
// offer it as a selectable integer anyway — the node's type is the element
// type — which let an administrator save a mapping that failed at every
// render. Array elements are now non-selectable at the source, so the two
// stages agree and the failure lands at save time, naming the field.
func TestArrayProjectionRejectedAtSaveTime(t *testing.T) {
	const body = `{"reviews":[{"rating":5},{"rating":3}]}`

	set := []mappings.FieldMapping{
		{OutputName: "rating", SourcePath: "reviews[].rating", DataType: mappings.TypeInteger},
	}

	err := mappings.ValidateAgainstSchema(infer(t, body), set)
	if !errors.Is(err, mappings.ErrUnknownSourcePath) {
		t.Fatalf("ValidateAgainstSchema = %v, want %v", err, mappings.ErrUnknownSourcePath)
	}

	// The runtime half of the agreement: had it been saved anyway, Apply
	// would still refuse it rather than emit a list into the template.
	response, decErr := responseparser.Decode([]byte(body))
	if decErr != nil {
		t.Fatalf("decode: %v", decErr)
	}
	if _, err := mappings.Apply(set, response); !errors.Is(err, mappings.ErrWrongRuntimeType) {
		t.Fatalf("Apply error = %v, want %v", err, mappings.ErrWrongRuntimeType)
	}
}

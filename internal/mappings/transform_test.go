package mappings_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"content-pipeline-insider/internal/mappings"
	"content-pipeline-insider/internal/responseparser"
)

// decode runs a fixture through the same path a live response takes. Building
// the map by hand would silently drop the UseNumber contract that integer
// coercion depends on, and the tests would then pass against a shape the
// runtime never sees.
func decode(t *testing.T, body string) any {
	t.Helper()
	v, err := responseparser.Decode([]byte(body))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return v
}

const productBody = `{
	"product": {
		"name": "Kettle",
		"stock": 12,
		"price": 249.90,
		"inStock": true,
		"discount": null
	},
	"order-id": "A-71",
	"reviews": [
		{"rating": 5},
		{"rating": 3}
	]
}`

func TestApply(t *testing.T) {
	tests := []struct {
		name string
		set  []mappings.FieldMapping
		want map[string]any
	}{
		{
			name: "scalars of every declared type",
			set: []mappings.FieldMapping{
				{OutputName: "productName", SourcePath: "product.name", DataType: mappings.TypeString},
				{OutputName: "stock", SourcePath: "product.stock", DataType: mappings.TypeInteger},
				{OutputName: "price", SourcePath: "product.price", DataType: mappings.TypeNumber},
				{OutputName: "inStock", SourcePath: "product.inStock", DataType: mappings.TypeBoolean},
			},
			want: map[string]any{
				"productName": "Kettle",
				"stock":       int64(12),
				"price":       249.90,
				"inStock":     true,
			},
		},
		{
			name: "integer widened to number",
			set: []mappings.FieldMapping{
				{OutputName: "stock", SourcePath: "product.stock", DataType: mappings.TypeNumber},
			},
			want: map[string]any{"stock": float64(12)},
		},
		{
			name: "quoted key",
			set: []mappings.FieldMapping{
				{OutputName: "orderID", SourcePath: `"order-id"`, DataType: mappings.TypeString},
			},
			want: map[string]any{"orderID": "A-71"},
		},
		{
			name: "absent optional field is omitted entirely",
			set: []mappings.FieldMapping{
				{OutputName: "productName", SourcePath: "product.name", DataType: mappings.TypeString},
				{OutputName: "colour", SourcePath: "product.colour", DataType: mappings.TypeString},
			},
			want: map[string]any{"productName": "Kettle"},
		},
		{
			name: "explicit null is treated as absent",
			set: []mappings.FieldMapping{
				{OutputName: "discount", SourcePath: "product.discount", DataType: mappings.TypeNumber},
			},
			want: map[string]any{},
		},
		{
			name: "default fills an absent field",
			set: []mappings.FieldMapping{
				{
					OutputName:   "colour",
					SourcePath:   "product.colour",
					DataType:     mappings.TypeString,
					DefaultValue: json.RawMessage(`"unspecified"`),
				},
			},
			want: map[string]any{"colour": "unspecified"},
		},
		{
			name: "default fills an explicit null",
			set: []mappings.FieldMapping{
				{
					OutputName:   "discount",
					SourcePath:   "product.discount",
					DataType:     mappings.TypeInteger,
					DefaultValue: json.RawMessage(`0`),
				},
			},
			want: map[string]any{"discount": int64(0)},
		},
		{
			name: "a present value beats the default",
			set: []mappings.FieldMapping{
				{
					OutputName:   "stock",
					SourcePath:   "product.stock",
					DataType:     mappings.TypeInteger,
					DefaultValue: json.RawMessage(`0`),
				},
			},
			want: map[string]any{"stock": int64(12)},
		},
		{
			name: "unselected fields do not travel downstream",
			set: []mappings.FieldMapping{
				{OutputName: "productName", SourcePath: "product.name", DataType: mappings.TypeString},
			},
			want: map[string]any{"productName": "Kettle"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mappings.Apply(tc.set, decode(t, productBody))
			if err != nil {
				t.Fatalf("Apply: unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Apply =\n  %#v\nwant\n  %#v", got, tc.want)
			}
		})
	}
}

func TestApplyErrors(t *testing.T) {
	tests := []struct {
		name string
		set  []mappings.FieldMapping
		want error
	}{
		{
			name: "required field absent",
			set: []mappings.FieldMapping{
				{OutputName: "colour", SourcePath: "product.colour", DataType: mappings.TypeString, Required: true},
			},
			want: mappings.ErrRequiredFieldMissing,
		},
		{
			name: "required field present but null",
			set: []mappings.FieldMapping{
				{OutputName: "discount", SourcePath: "product.discount", DataType: mappings.TypeNumber, Required: true},
			},
			want: mappings.ErrRequiredFieldMissing,
		},
		{
			name: "number declared string",
			set: []mappings.FieldMapping{
				{OutputName: "stock", SourcePath: "product.stock", DataType: mappings.TypeString},
			},
			want: mappings.ErrWrongRuntimeType,
		},
		{
			name: "string declared integer",
			set: []mappings.FieldMapping{
				{OutputName: "productName", SourcePath: "product.name", DataType: mappings.TypeInteger},
			},
			want: mappings.ErrWrongRuntimeType,
		},
		{
			name: "fractional value declared integer",
			set: []mappings.FieldMapping{
				{OutputName: "price", SourcePath: "product.price", DataType: mappings.TypeInteger},
			},
			want: mappings.ErrWrongRuntimeType,
		},
		{
			name: "boolean declared string",
			set: []mappings.FieldMapping{
				{OutputName: "inStock", SourcePath: "product.inStock", DataType: mappings.TypeString},
			},
			want: mappings.ErrWrongRuntimeType,
		},
		{
			name: "object declared string",
			set: []mappings.FieldMapping{
				{OutputName: "product", SourcePath: "product", DataType: mappings.TypeString},
			},
			want: mappings.ErrWrongRuntimeType,
		},
		{
			name: "default of the wrong type",
			set: []mappings.FieldMapping{
				{
					OutputName:   "colour",
					SourcePath:   "product.colour",
					DataType:     mappings.TypeString,
					DefaultValue: json.RawMessage(`false`),
				},
			},
			want: mappings.ErrBadDefault,
		},
		{
			name: "default that is not valid JSON",
			set: []mappings.FieldMapping{
				{
					OutputName:   "colour",
					SourcePath:   "product.colour",
					DataType:     mappings.TypeString,
					DefaultValue: json.RawMessage(`"unterminated`),
				},
			},
			want: mappings.ErrBadDefault,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mappings.Apply(tc.set, decode(t, productBody))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Apply error = %v, want %v", err, tc.want)
			}
			if got != nil {
				t.Errorf("Apply returned %#v alongside an error; a partial object must never reach the renderer", got)
			}
		})
	}
}

// TestApplyDoesNotMutateResponse is the guarantee that lets the same decoded
// response be handed to more than one pipeline: Apply builds a new map, so a
// caller's copy is still the partner's copy afterwards.
func TestApplyDoesNotMutateResponse(t *testing.T) {
	response := decode(t, productBody)

	// A second, independent decode of the same bytes is the reference. Deep
	// equality against it proves nothing was added, removed, or coerced in
	// place, which a shallow snapshot of the top-level map would miss.
	reference := decode(t, productBody)

	set := []mappings.FieldMapping{
		{OutputName: "productName", SourcePath: "product.name", DataType: mappings.TypeString},
		{OutputName: "stock", SourcePath: "product.stock", DataType: mappings.TypeInteger},
		{
			OutputName:   "colour",
			SourcePath:   "product.colour",
			DataType:     mappings.TypeString,
			DefaultValue: json.RawMessage(`"unspecified"`),
		},
	}

	out, err := mappings.Apply(set, response)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !reflect.DeepEqual(response, reference) {
		t.Fatalf("Apply mutated the response:\n got %#v\nwant %#v", response, reference)
	}

	// Writing through the result must not reach the response either, which is
	// what would happen if Apply had aliased a subtree instead of copying.
	out["productName"] = "tampered"
	if !reflect.DeepEqual(response, reference) {
		t.Errorf("writing to Apply's result reached the response: %#v", response)
	}
}

// TestApplyIntegerFidelity pins the reason responseparser decodes with
// UseNumber. Through float64 an ID this large loses its last digits, and the
// email links to the wrong product.
func TestApplyIntegerFidelity(t *testing.T) {
	const id = 9007199254740993 // 2^53 + 1, the first integer float64 cannot hold
	response := decode(t, `{"productId": 9007199254740993}`)

	out, err := mappings.Apply([]mappings.FieldMapping{
		{OutputName: "productId", SourcePath: "productId", DataType: mappings.TypeInteger},
	}, response)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, ok := out["productId"].(int64)
	if !ok {
		t.Fatalf("productId is %T, want int64", out["productId"])
	}
	if got != id {
		t.Errorf("productId = %d, want %d", got, id)
	}
}

// TestApplyRejectsOutOfRangeInteger is the same boundary from the other side:
// a value no int64 can hold is a genuine mismatch with a field declared
// integer, not something to round.
func TestApplyRejectsOutOfRangeInteger(t *testing.T) {
	response := decode(t, `{"productId": 99999999999999999999}`)

	_, err := mappings.Apply([]mappings.FieldMapping{
		{OutputName: "productId", SourcePath: "productId", DataType: mappings.TypeInteger},
	}, response)
	if !errors.Is(err, mappings.ErrWrongRuntimeType) {
		t.Fatalf("Apply error = %v, want %v", err, mappings.ErrWrongRuntimeType)
	}
}

func TestApplyEmptySet(t *testing.T) {
	// Apply is not the guard for an empty selection — ValidateSet is — so an
	// empty set here is simply an empty object rather than an error.
	out, err := mappings.Apply(nil, decode(t, productBody))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Apply(nil) = %#v, want an empty object", out)
	}
}

func TestApplyInvalidExpression(t *testing.T) {
	_, err := mappings.Apply([]mappings.FieldMapping{
		{OutputName: "broken", SourcePath: "product..name", DataType: mappings.TypeString},
	}, decode(t, productBody))
	if err == nil {
		t.Fatal("Apply accepted an unparseable JMESPath expression")
	}
}

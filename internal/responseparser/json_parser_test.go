package responseparser

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	t.Run("object", func(t *testing.T) {
		got, err := Decode([]byte(`{"product":{"name":"Acme Motor"}}`))
		if err != nil {
			t.Fatalf("Decode() = %v", err)
		}
		obj, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("Decode() = %T, want map[string]any", got)
		}
		if _, present := obj["product"]; !present {
			t.Errorf("Decode() = %#v, want a product key", obj)
		}
	})

	t.Run("array", func(t *testing.T) {
		got, err := Decode([]byte(`[1,2,3]`))
		if err != nil {
			t.Fatalf("Decode() = %v", err)
		}
		if arr, ok := got.([]any); !ok || len(arr) != 3 {
			t.Fatalf("Decode() = %#v, want a 3-element slice", got)
		}
	})

	t.Run("bare scalar", func(t *testing.T) {
		if _, err := Decode([]byte(`"just a string"`)); err != nil {
			t.Fatalf("Decode() = %v, want nil", err)
		}
	})
}

func TestDecodeRejects(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr error
	}{
		{"empty", ``, ErrEmptyBody},
		{"whitespace only", "  \n\t ", ErrEmptyBody},
		{"truncated object", `{"a":`, ErrInvalidJSON},
		{"not json at all", `<html><body>502</body></html>`, ErrInvalidJSON},
		// Two JSON values back to back. Accepting the first and ignoring
		// the rest would silently parse half of a malformed response.
		{"trailing value", `{"a":1} {"b":2}`, ErrInvalidJSON},
		{"trailing garbage", `{"a":1} oops`, ErrInvalidJSON},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode([]byte(tc.body)); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Decode(%q) = %v, want %v", tc.body, err, tc.wantErr)
			}
		})
	}
}

// UseNumber keeps the original text of every number. Without it, decoding
// into float64 silently rounds identifiers and prices — a product id of
// 12345678901234567890 would come back as a different number.
func TestDecodePreservesNumericPrecision(t *testing.T) {
	got, err := Decode([]byte(`{"id":12345678901234567890,"price":249.99}`))
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	obj := got.(map[string]any)

	id, ok := obj["id"].(json.Number)
	if !ok {
		t.Fatalf("id = %T, want json.Number", obj["id"])
	}
	if id.String() != "12345678901234567890" {
		t.Errorf("id = %s, want the digits preserved exactly", id.String())
	}

	price, ok := obj["price"].(json.Number)
	if !ok {
		t.Fatalf("price = %T, want json.Number", obj["price"])
	}
	if price.String() != "249.99" {
		t.Errorf("price = %s, want 249.99", price.String())
	}
}

func nestObjects(depth int) string {
	return strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth)
}

func TestDecodeDepthLimit(t *testing.T) {
	t.Run("at the limit", func(t *testing.T) {
		if _, err := Decode([]byte(nestObjects(MaxDepth))); err != nil {
			t.Fatalf("Decode(depth %d) = %v, want nil", MaxDepth, err)
		}
	})

	t.Run("past the limit", func(t *testing.T) {
		if _, err := Decode([]byte(nestObjects(MaxDepth + 1))); !errors.Is(err, ErrTooDeep) {
			t.Fatalf("Decode(depth %d) = %v, want %v", MaxDepth+1, err, ErrTooDeep)
		}
	})

	t.Run("deeply nested arrays are caught too", func(t *testing.T) {
		body := strings.Repeat("[", MaxDepth+5) + "1" + strings.Repeat("]", MaxDepth+5)
		if _, err := Decode([]byte(body)); !errors.Is(err, ErrTooDeep) {
			t.Fatalf("Decode(nested arrays) = %v, want %v", err, ErrTooDeep)
		}
	})
}

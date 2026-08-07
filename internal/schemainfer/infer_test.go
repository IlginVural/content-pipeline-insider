package schemainfer

import (
	"errors"
	"testing"

	"content-pipeline-insider/internal/responseparser"
)

// decode builds samples the way the service does, so these tests exercise
// the json.Number values Infer actually receives rather than the float64s
// a plain json.Unmarshal would produce.
func decode(t *testing.T, body string) any {
	t.Helper()
	value, err := responseparser.Decode([]byte(body))
	if err != nil {
		t.Fatalf("decode(%s) = %v", body, err)
	}
	return value
}

func child(t *testing.T, node *SchemaNode, name string) *SchemaNode {
	t.Helper()
	for i := range node.Children {
		if node.Children[i].Name == name {
			return &node.Children[i]
		}
	}
	t.Fatalf("no child %q in %v", name, childNames(node))
	return nil
}

func childNames(node *SchemaNode) []string {
	out := make([]string, 0, len(node.Children))
	for _, c := range node.Children {
		out = append(out, c.Name)
	}
	return out
}

func TestInferREADMEExample(t *testing.T) {
	const body = `{
	  "product": {
	    "name": "Acme Motor",
	    "inventory": {"available": 121, "reserved": 20},
	    "pricing": {"current": 249.99},
	    "supplier": {"email": "private@example.com"}
	  }
	}`

	root, err := Infer([]any{decode(t, body)})
	if err != nil {
		t.Fatalf("Infer() = %v", err)
	}

	if root.Type != TypeObject {
		t.Errorf("root type = %s, want object", root.Type)
	}
	// Containers are not selectable: selecting one would copy an entire
	// subtree into the normalized output, including fields the admin
	// never reviewed.
	if root.Selectable {
		t.Error("root is selectable, want false — containers cannot be selected")
	}

	product := child(t, root, "product")
	if product.Selectable {
		t.Error("product is selectable, want false")
	}

	// Children are sorted so the admin's field tree is stable across
	// calls; Go map iteration order is randomized.
	want := []string{"inventory", "name", "pricing", "supplier"}
	got := childNames(product)
	if len(got) != len(want) {
		t.Fatalf("product children = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("product children = %v, want %v (sorted)", got, want)
		}
	}

	available := child(t, child(t, product, "inventory"), "available")
	if available.Type != TypeInteger {
		t.Errorf("available type = %s, want integer", available.Type)
	}
	if !available.Selectable {
		t.Error("available is not selectable, want true")
	}
	// jmesPath is what gets stored as sourcePath when the admin selects
	// the field, so it has to be directly usable by the transformer.
	if available.JMESPath != "product.inventory.available" {
		t.Errorf("available jmesPath = %q, want product.inventory.available", available.JMESPath)
	}
	if available.JSONPath != "$.product.inventory.available" {
		t.Errorf("available jsonPath = %q, want $.product.inventory.available", available.JSONPath)
	}

	current := child(t, child(t, product, "pricing"), "current")
	if current.Type != TypeNumber {
		t.Errorf("current type = %s, want number (it has a decimal point)", current.Type)
	}

	// Every leaf in this response is selectable; the containers are not.
	if leaves := Flatten(root); len(leaves) != 5 {
		names := make([]string, 0, len(leaves))
		for _, l := range leaves {
			names = append(names, l.JMESPath)
		}
		t.Errorf("Flatten() = %v, want 5 selectable leaves", names)
	}
}

func TestInferTypes(t *testing.T) {
	root, err := Infer([]any{decode(t, `{
	  "count": 121,
	  "price": 249.99,
	  "exponent": 1e5,
	  "negative": -3,
	  "name": "Acme",
	  "active": true,
	  "missing": null
	}`)})
	if err != nil {
		t.Fatalf("Infer() = %v", err)
	}

	cases := map[string]Type{
		"count":    TypeInteger,
		"price":    TypeNumber,
		"exponent": TypeNumber,
		"negative": TypeInteger,
		"name":     TypeString,
		"active":   TypeBoolean,
		"missing":  TypeNull,
	}
	for name, want := range cases {
		if got := child(t, root, name).Type; got != want {
			t.Errorf("%s type = %s, want %s", name, got, want)
		}
	}

	// A lone null says the field exists but nothing about what it
	// normally holds, so it is reported nullable rather than guessed.
	if !child(t, root, "missing").Nullable {
		t.Error("missing is not nullable, want true")
	}
}

func TestInferArrays(t *testing.T) {
	t.Run("array of objects", func(t *testing.T) {
		root, err := Infer([]any{decode(t, `{"items":[{"id":1},{"id":2}]}`)})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}

		items := child(t, root, "items")
		if items.Type != TypeArray {
			t.Fatalf("items type = %s, want array", items.Type)
		}
		if items.Selectable {
			t.Error("items is selectable, want false")
		}
		if items.ArrayItem == nil {
			t.Fatal("items has no ArrayItem")
		}
		if items.ArrayItem.Type != TypeObject {
			t.Errorf("item type = %s, want object", items.ArrayItem.Type)
		}
		if got := child(t, items.ArrayItem, "id").JMESPath; got != "items[].id" {
			t.Errorf("id jmesPath = %q, want items[].id", got)
		}
	})

	t.Run("array elements are described but not selectable", func(t *testing.T) {
		// items[].id is a projection: it evaluates to every id, and the
		// transformation stage has no data type for a list. Offering it
		// would let an admin save a mapping that fails at every render.
		root, err := Infer([]any{decode(t, `{"items":[{"id":1,"tag":"a"}],"name":"Acme"}`)})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}

		item := child(t, root, "items").ArrayItem
		for _, name := range []string{"id", "tag"} {
			if child(t, item, name).Selectable {
				t.Errorf("items[].%s is selectable, want false", name)
			}
		}

		// The shape is still described, so the admin can see what the
		// array holds — it just cannot be picked.
		if got := child(t, item, "id").JMESPath; got != "items[].id" {
			t.Errorf("id jmesPath = %q, want items[].id", got)
		}

		leaves := Flatten(root)
		if len(leaves) != 1 || leaves[0].JMESPath != "name" {
			got := make([]string, 0, len(leaves))
			for _, l := range leaves {
				got = append(got, l.JMESPath)
			}
			t.Errorf("Flatten() = %v, want only [name]", got)
		}
	})

	t.Run("an array of scalars is not selectable either", func(t *testing.T) {
		root, err := Infer([]any{decode(t, `{"tags":["new","sale"]}`)})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		if item := child(t, root, "tags").ArrayItem; item.Selectable {
			t.Error("tags[] is selectable, want false")
		}
		if leaves := Flatten(root); len(leaves) != 0 {
			t.Errorf("Flatten() = %v, want none", leaves)
		}
	})

	t.Run("nested arrays are cleared all the way down", func(t *testing.T) {
		root, err := Infer([]any{decode(t, `{"orders":[{"lines":[{"sku":"X1"}]}]}`)})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		lines := child(t, child(t, root, "orders").ArrayItem, "lines")
		if sku := child(t, lines.ArrayItem, "sku"); sku.Selectable {
			t.Error("orders[].lines[].sku is selectable, want false")
		}
		if leaves := Flatten(root); len(leaves) != 0 {
			t.Errorf("Flatten() = %v, want none", leaves)
		}
	})

	t.Run("a field typed only by a later sample stays unselectable", func(t *testing.T) {
		// merge adopts the other sample's Selectable when one side was
		// null, which is why the flag is cleared after merging rather
		// than before.
		root, err := Infer([]any{
			decode(t, `{"items":[{"id":null}]}`),
			decode(t, `{"items":[{"id":7}]}`),
		})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		id := child(t, child(t, root, "items").ArrayItem, "id")
		if id.Type != TypeInteger {
			t.Errorf("id type = %s, want integer", id.Type)
		}
		if id.Selectable {
			t.Error("items[].id is selectable after merging, want false")
		}
	})

	t.Run("empty array reveals nothing about its elements", func(t *testing.T) {
		root, err := Infer([]any{decode(t, `{"items":[]}`)})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		item := child(t, root, "items").ArrayItem
		if item == nil {
			t.Fatal("items has no ArrayItem")
		}
		if item.Type != TypeNull || !item.Nullable {
			t.Errorf("item = %s nullable=%v, want null and nullable", item.Type, item.Nullable)
		}
	})

	t.Run("elements of differing types are flagged", func(t *testing.T) {
		root, err := Infer([]any{decode(t, `{"items":[1,"two"]}`)})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		item := child(t, root, "items").ArrayItem
		if item.Type != TypeMixed {
			t.Fatalf("item type = %s, want mixed", item.Type)
		}
		if len(item.MixedTypes) != 2 {
			t.Errorf("mixedTypes = %v, want integer and string", item.MixedTypes)
		}
	})
}

// Merging is what makes discovery honest across more than one test call:
// a field that is null in one response and a string in the next is a
// nullable string, not a guess based on whichever arrived first.
func TestInferMergesSamples(t *testing.T) {
	t.Run("null in one sample widens to the other type", func(t *testing.T) {
		root, err := Infer([]any{
			decode(t, `{"name":null}`),
			decode(t, `{"name":"Acme"}`),
		})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		name := child(t, root, "name")
		if name.Type != TypeString {
			t.Errorf("name type = %s, want string", name.Type)
		}
		if !name.Nullable {
			t.Error("name is not nullable, want true")
		}
		if !name.Selectable {
			t.Error("name is not selectable, want true")
		}
	})

	t.Run("order does not matter", func(t *testing.T) {
		root, err := Infer([]any{
			decode(t, `{"name":"Acme"}`),
			decode(t, `{"name":null}`),
		})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		name := child(t, root, "name")
		if name.Type != TypeString || !name.Nullable {
			t.Errorf("name = %s nullable=%v, want string and nullable", name.Type, name.Nullable)
		}
	})

	t.Run("genuinely inconsistent types are flagged, not chosen between", func(t *testing.T) {
		root, err := Infer([]any{
			decode(t, `{"stock":10}`),
			decode(t, `{"stock":"unknown"}`),
		})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		stock := child(t, root, "stock")
		if stock.Type != TypeMixed {
			t.Fatalf("stock type = %s, want mixed", stock.Type)
		}
		if len(stock.MixedTypes) != 2 {
			t.Errorf("stock mixedTypes = %v, want both observed types", stock.MixedTypes)
		}
	})

	t.Run("a field present in only one sample is kept but nullable", func(t *testing.T) {
		root, err := Infer([]any{
			decode(t, `{"name":"Acme"}`),
			decode(t, `{"name":"Acme","discount":5}`),
		})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		discount := child(t, root, "discount")
		if discount.Type != TypeInteger {
			t.Errorf("discount type = %s, want integer", discount.Type)
		}
		if !discount.Nullable {
			t.Error("discount is not nullable, want true — it is not always sent")
		}
	})
}

// Without quoting, a field named "order-id" parses as subtraction and a
// field named "1st" does not parse at all.
func TestInferQuotesNonIdentifierKeys(t *testing.T) {
	cases := []struct {
		body string
		key  string
		want string
	}{
		{`{"order-id":1}`, "order-id", `"order-id"`},
		{`{"1st":1}`, "1st", `"1st"`},
		{`{"with space":1}`, "with space", `"with space"`},
		{`{"":1}`, "", `""`},
		{`{"plain":1}`, "plain", "plain"},
		{`{"_leading":1}`, "_leading", "_leading"},
		{`{"mixed1":1}`, "mixed1", "mixed1"},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			root, err := Infer([]any{decode(t, tc.body)})
			if err != nil {
				t.Fatalf("Infer() = %v", err)
			}
			if got := child(t, root, tc.key).JMESPath; got != tc.want {
				t.Errorf("jmesPath = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("nested under a plain key", func(t *testing.T) {
		root, err := Infer([]any{decode(t, `{"order":{"order-id":1}}`)})
		if err != nil {
			t.Fatalf("Infer() = %v", err)
		}
		got := child(t, child(t, root, "order"), "order-id").JMESPath
		if want := `order."order-id"`; got != want {
			t.Errorf("jmesPath = %q, want %q", got, want)
		}
	})
}

func TestInferNoSamples(t *testing.T) {
	if _, err := Infer(nil); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Infer(nil) = %v, want %v", err, ErrNoSamples)
	}
}

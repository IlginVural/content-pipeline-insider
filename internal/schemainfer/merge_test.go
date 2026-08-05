package schemainfer

import (
	"strings"
	"testing"
)

func TestMergeNilOperands(t *testing.T) {
	node := SchemaNode{Name: "name", Type: TypeString, Selectable: true}

	t.Run("both nil", func(t *testing.T) {
		if got := merge(nil, nil); got.Type != "" || got.Name != "" {
			t.Fatalf("merge(nil, nil) = %#v, want the zero node", got)
		}
	})

	t.Run("left nil", func(t *testing.T) {
		if got := merge(nil, &node); got.Type != TypeString || got.Name != "name" {
			t.Fatalf("merge(nil, b) = %#v, want b", got)
		}
	})

	t.Run("right nil", func(t *testing.T) {
		if got := merge(&node, nil); got.Type != TypeString || got.Name != "name" {
			t.Fatalf("merge(a, nil) = %#v, want a", got)
		}
	})
}

func TestMergeAccumulatesMixedTypes(t *testing.T) {
	// Three samples disagreeing pairwise: the union has to collect every
	// observed type, not just the last two compared.
	root, err := Infer([]any{
		decode(t, `{"stock":10}`),
		decode(t, `{"stock":"unknown"}`),
		decode(t, `{"stock":true}`),
	})
	if err != nil {
		t.Fatalf("Infer() = %v", err)
	}

	stock := child(t, root, "stock")
	if stock.Type != TypeMixed {
		t.Fatalf("stock type = %s, want mixed", stock.Type)
	}
	if len(stock.MixedTypes) != 3 {
		t.Errorf("stock mixedTypes = %v, want integer, string and boolean", stock.MixedTypes)
	}
	// TypeMixed is the flag itself, not one of the observed types.
	for _, ty := range stock.MixedTypes {
		if ty == TypeMixed {
			t.Errorf("mixedTypes = %v, should not contain %q itself", stock.MixedTypes, TypeMixed)
		}
	}
}

// String renders the field tree the administrator picks from, so it is
// product surface rather than debug output.
func TestSchemaNodeString(t *testing.T) {
	root, err := Infer([]any{decode(t, `{
	  "product": {
	    "name": "Acme Motor",
	    "inventory": {"available": 121}
	  },
	  "tags": []
	}`)})
	if err != nil {
		t.Fatalf("Infer() = %v", err)
	}

	out := root.String()

	for _, want := range []string{
		"product",
		"inventory",
		"available",
		"integer",
		// Selectable leaves show the expression that will be stored as
		// sourcePath, so the admin can see what they are picking.
		"product.inventory.available",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("String() missing %q:\n%s", want, out)
		}
	}

	// Containers must not be marked selectable in the rendered tree.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "product ") && strings.Contains(line, "•") {
			t.Errorf("container line marked selectable: %q", line)
		}
	}
}

func TestSchemaNodeStringFlagsMixedTypes(t *testing.T) {
	root, err := Infer([]any{
		decode(t, `{"stock":10}`),
		decode(t, `{"stock":"unknown"}`),
	})
	if err != nil {
		t.Fatalf("Infer() = %v", err)
	}

	// A field whose type is inconsistent across samples is the one thing
	// an admin most needs to notice before selecting it.
	if out := root.String(); !strings.Contains(out, "mixed") {
		t.Errorf("String() does not flag the mixed field:\n%s", out)
	}
}

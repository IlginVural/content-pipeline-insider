package schemainfer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const MaxArraySamples = 20

// Infer describes the shape of one or more decoded responses.

func Infer(samples []any) (*SchemaNode, error) {
	if len(samples) == 0 {
		return nil, ErrNoSamples
	}

	node := inferValue("", "$", "", samples[0])
	for _, extra := range samples[1:] {
		other := inferValue("", "$", "", extra)
		node = merge(&node, &other)
	}
	return &node, nil
}

// inferValue describes a single value.
//
// jsonPath is the human-readable location ("$.dimensions.width") shown
// in the tree; jmesPath is the machine expression ("dimensions.width")
// the transformer will evaluate. They are built in parallel because
// they diverge on arrays and on keys needing quoting.
func inferValue(name, jsonPath, jmesPath string, value any) SchemaNode {
	node := SchemaNode{
		Name:     name,
		JSONPath: jsonPath,
		JMESPath: jmesPath,
	}

	switch v := value.(type) {
	case nil:
		// A null tells us the field exists but nothing about what it
		// normally holds. Reporting "null" is honest; guessing a type
		// from a single null observation would not be.
		node.Type = TypeNull
		node.Nullable = true
		node.Selectable = true

	case bool:
		node.Type = TypeBoolean
		node.Selectable = true
		node.SampleValue = v

	case string:
		node.Type = TypeString
		node.Selectable = true
		node.SampleValue = v

	case json.Number:
		node.Type = numberType(v)
		node.Selectable = true
		node.SampleValue = v

	case map[string]any:
		node.Type = TypeObject
		node.Selectable = false
		node.Children = inferObjectChildren(jsonPath, jmesPath, v)

	case []any:
		node.Type = TypeArray
		node.Selectable = false
		node.ArrayItem = inferArrayItem(jsonPath, jmesPath, v)

	default:
		// Unreachable for values produced by responseparser.Decode,
		// which yields only the cases above. Present so an unexpected
		// input surfaces as a visible type rather than a silent zero.
		node.Type = TypeMixed
	}

	return node
}

func numberType(n json.Number) Type {
	s := n.String()
	if strings.ContainsAny(s, ".eE") {
		return TypeNumber
	}
	return TypeInteger
}

func inferObjectChildren(jsonPath, jmesPath string, obj map[string]any) []SchemaNode {
	// Sorted so the tree is stable across runs. Go map iteration is
	// randomized, and a field list that reorders itself on every call
	// is unusable in a UI and untestable in a test.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	children := make([]SchemaNode, 0, len(keys))
	for _, k := range keys {
		children = append(children, inferValue(
			k,
			jsonPath+"."+k,
			joinJMES(jmesPath, k),
			obj[k],
		))
	}
	return children
}

// inferArrayItem describes what an array holds by merging its elements.

func inferArrayItem(jsonPath, jmesPath string, arr []any) *SchemaNode {
	if len(arr) == 0 {
		// An empty array reveals nothing about its element type. Saying
		// so beats inventing a shape that the next response contradicts.
		return &SchemaNode{
			Name:     "[]",
			JSONPath: jsonPath + "[*]",
			JMESPath: jmesPath + "[]",
			Type:     TypeNull,
			Nullable: true,
		}
	}

	limit := len(arr)
	if limit > MaxArraySamples {
		limit = MaxArraySamples
	}

	item := inferValue("[]", jsonPath+"[*]", jmesPath+"[]", arr[0])
	for i := 1; i < limit; i++ {
		other := inferValue("[]", jsonPath+"[*]", jmesPath+"[]", arr[i])
		item = merge(&item, &other)
	}
	return &item
}

// joinJMES appends a key to a JMESPath expression, quoting the key when
// it is not a bare identifier. Without quoting, a field named
// "order-id" would parse as subtraction and a field named "1st" would
// not parse at all.
func joinJMES(base, key string) string {
	if !isBareIdentifier(key) {
		key = `"` + strings.ReplaceAll(key, `"`, `\"`) + `"`
	}
	if base == "" {
		return key
	}
	return base + "." + key
}

func isBareIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Flatten returns every selectable node in the tree, depth-first.
// Useful for printing a flat pick-list and for checking that a
// selected sourcePath actually exists in the discovered schema.
func Flatten(node *SchemaNode) []SchemaNode {
	if node == nil {
		return nil
	}
	var out []SchemaNode
	if node.Selectable && node.JMESPath != "" {
		out = append(out, *node)
	}
	for i := range node.Children {
		out = append(out, Flatten(&node.Children[i])...)
	}
	if node.ArrayItem != nil {
		out = append(out, Flatten(node.ArrayItem)...)
	}
	return out
}

// String renders the tree the way the README draws it, for CLI output.
func (n *SchemaNode) String() string {
	var b strings.Builder
	n.write(&b, 0)
	return b.String()
}

func (n *SchemaNode) write(b *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)

	name := n.Name
	if name == "" {
		name = "(root)"
	}

	marker := " "
	if n.Selectable {
		marker = "•"
	}

	fmt.Fprintf(b, "%s%s %-24s %-8s", indent, marker, name, n.Type)
	if n.Nullable {
		b.WriteString(" nullable")
	}
	if len(n.MixedTypes) > 0 {
		fmt.Fprintf(b, "  ⚠ mixed: %v", n.MixedTypes)
	}
	if n.Selectable && n.JMESPath != "" {
		fmt.Fprintf(b, "\n%s    → %s", indent, n.JMESPath)
	}
	b.WriteString("\n")

	for i := range n.Children {
		n.Children[i].write(b, depth+1)
	}
	if n.ArrayItem != nil {
		n.ArrayItem.write(b, depth+1)
	}
}

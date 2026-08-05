package schemainfer

// merge combines two descriptions of the same location into one.

func merge(a, b *SchemaNode) SchemaNode {
	if a == nil && b == nil {
		return SchemaNode{}
	}
	if a == nil {
		return *b
	}
	if b == nil {
		return *a
	}

	out := *a
	out.Nullable = a.Nullable || b.Nullable

	switch {
	case a.Type == b.Type:
		// Same type — nothing to reconcile.

	case a.Type == TypeNull:
		// The other sample saw a real type; adopt it and remember that
		// this field can be null.
		out.Type = b.Type
		out.Selectable = b.Selectable
		out.Children = b.Children
		out.ArrayItem = b.ArrayItem
		out.Nullable = true
		if out.SampleValue == nil {
			out.SampleValue = b.SampleValue
		}

	case b.Type == TypeNull:
		out.Nullable = true

	default:
		// Genuinely inconsistent — 10 in one sample, "unknown" in
		// another. Flag it rather than choosing; a template written
		// against either assumption will break on the other.
		out.Type = TypeMixed
		out.MixedTypes = unionTypes(a, b)
		out.Selectable = a.Selectable && b.Selectable
	}

	if len(a.Children) > 0 || len(b.Children) > 0 {
		out.Children = mergeChildren(a.Children, b.Children)
	}
	if a.ArrayItem != nil || b.ArrayItem != nil {
		item := merge(a.ArrayItem, b.ArrayItem)
		out.ArrayItem = &item
	}
	return out
}

// mergeChildren unions two child lists by name, preserving order and
// keeping fields that appear in only one sample.
func mergeChildren(a, b []SchemaNode) []SchemaNode {
	index := make(map[string]int, len(a))
	out := make([]SchemaNode, 0, len(a)+len(b))

	for _, child := range a {
		index[child.Name] = len(out)
		out = append(out, child)
	}

	for i := range b {
		child := b[i]
		if pos, ok := index[child.Name]; ok {
			out[pos] = merge(&out[pos], &child)
			continue
		}
		// Present in one sample only. Keep it — the field is real —
		// but nullable, since it is evidently not always sent.
		child.Nullable = true
		index[child.Name] = len(out)
		out = append(out, child)
	}
	return out
}

func unionTypes(a, b *SchemaNode) []Type {
	seen := map[Type]bool{}
	var out []Type
	add := func(ts ...Type) {
		for _, t := range ts {
			if t == "" || t == TypeMixed || seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	add(a.MixedTypes...)
	add(a.Type)
	add(b.MixedTypes...)
	add(b.Type)
	return out
}

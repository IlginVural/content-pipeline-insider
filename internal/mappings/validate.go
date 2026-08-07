package mappings

import (
	"fmt"

	"content-pipeline-insider/internal/schemainfer"
)

// ValidateAgainstSchema confirms that every selected field really exists in
// the discovered response and holds the type the administrator declared.
//
// This is the one moment the discovered tree is load-bearing, and it is why
// the tree does not need to be persisted: it is consumed here, at save time,
// and then discarded. schemainfer.Flatten already exists for exactly this —
// see its doc comment.
func ValidateAgainstSchema(tree *schemainfer.SchemaNode, set []FieldMapping) error {
	if tree == nil {
		return schemainfer.ErrNoSamples
	}

	selectable := make(map[string]schemainfer.Type)
	for _, node := range schemainfer.Flatten(tree) {
		selectable[node.JMESPath] = node.Type
	}

	for _, m := range set {
		discovered, ok := selectable[m.SourcePath]
		if !ok {
			// The path was never in the response, or it names something
			// deliberately non-selectable: a container, because copying a
			// whole subtree would drag unselected fields along with it, or a
			// field below an array, because "reviews[].rating" is a
			// projection and Apply has no data type for the list it returns.
			return fmt.Errorf("%w: %q wants %s", ErrUnknownSourcePath, m.OutputName, m.SourcePath)
		}

		if discovered == schemainfer.TypeNull {
			return fmt.Errorf("%w: %s (field %q) — fetch another sample to determine it",
				ErrUntypedField, m.SourcePath, m.OutputName)
		}

		if !typeMatches(m.DataType, discovered) {
			return fmt.Errorf("%w: %q declares %s but %s is %s",
				ErrTypeMismatch, m.OutputName, m.DataType, m.SourcePath, discovered)
		}
	}

	return nil
}

// typeMatches allows an integer field to be declared as a number, because
// widening is always safe. The reverse is not: declaring integer over a field
// that has been seen with a fractional part would fail at the first render
// that hits one.
func typeMatches(declared DataType, discovered schemainfer.Type) bool {
	switch discovered {
	case schemainfer.TypeString:
		return declared == TypeString
	case schemainfer.TypeInteger:
		return declared == TypeInteger || declared == TypeNumber
	case schemainfer.TypeNumber:
		return declared == TypeNumber
	case schemainfer.TypeBoolean:
		return declared == TypeBoolean
	}
	// Objects, arrays, and mixed arrays are not selectable and never reach
	// here through Flatten, but false is the right answer if one ever does.
	return false
}

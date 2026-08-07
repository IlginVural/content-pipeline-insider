package schemainfer

// Package schemainfer walks a decoded partner response and describes
// its shape: what fields exist, where they live, what type they hold,
// and which of them an administrator may select.

type Type string

const (
	TypeObject  Type = "object"
	TypeArray   Type = "array"
	TypeString  Type = "string"
	TypeInteger Type = "integer"
	TypeNumber  Type = "number"
	TypeBoolean Type = "boolean"
	TypeNull    Type = "null"

	TypeMixed Type = "mixed" // used only for arrays, when elements have different types
)

// one field in the tree that will be displayed to admin
type SchemaNode struct {
	Name     string `json:"name"`
	JSONPath string `json:"jsonPath"`

	// JMESPath is the expression that extracts this field. It is what
	// gets stored as sourcePath when an administrator selects the
	// field, so it must be directly usable by the transformer.
	JMESPath string `json:"jmesPath"`

	Type     Type `json:"type"`
	Nullable bool `json:"nullable"`

	// Selectable answers whether an admin may pick this field for the
	// output. Two kinds of node are described but never selectable:
	// containers, because selecting one would copy an unreviewed subtree,
	// and anything below an array, because its expression is a projection
	// that yields a list no data type can hold.
	Selectable  bool `json:"selectable"`
	SampleValue any  `json:"sampleValue,omitempty"` // a value from the response, for display to the admin

	MixedTypes []Type       `json:"mixedTypes,omitempty"` // if TypeMixed, what types were found in the array
	Children   []SchemaNode `json:"children,omitempty"`   // if TypeObject, what fields exist inside it

	ArrayItem *SchemaNode `json:"arrayItem,omitempty"` // if TypeArray, what type of element is in the array

}

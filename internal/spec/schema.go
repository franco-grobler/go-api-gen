package spec

import "strings"

// Schema is a resolved JSON Schema node.
//
// A schema that was authored as a named component carries a non-zero Loc.
// Because the loader memoizes by Loc, every $ref to the same component yields
// the same *Schema pointer, no matter which file did the referencing. That
// identity is what allows a shared schema to be generated once and imported.
type Schema struct {
	Loc Location
	Pos pos

	// Type holds the JSON Schema types with "null" removed; Nullable records
	// whether "null" was present. OpenAPI 3.1 permits type to be a list.
	Type     []string
	Nullable bool
	Format   string

	Title       string
	Description string
	Deprecated  bool

	Default    any
	HasDefault bool
	Example    any
	HasExample bool
	Enum       []any
	Const      any
	HasConst   bool

	// Object
	Properties    []*Property
	Required      []string
	AdditionalPro *Schema // schema for additional properties, if constrained
	NoAdditional  bool    // additionalProperties: false
	MinProperties *uint64
	MaxProperties *uint64

	// Array
	Items       *Schema
	MinItems    *uint64
	MaxItems    *uint64
	UniqueItems bool

	// String
	MinLength *uint64
	MaxLength *uint64
	Pattern   string

	// Numeric
	Minimum          *float64
	Maximum          *float64
	ExclusiveMinimum *float64
	ExclusiveMaximum *float64
	MultipleOf       *float64

	// Composition
	AllOf         []*Schema
	OneOf         []*Schema
	AnyOf         []*Schema
	Not           *Schema
	Discriminator *Discriminator

	// Extensions
	GoType   string   // x-go-type: use this Go type verbatim
	GoImport string   // x-go-import: the import x-go-type needs
	GoName   string   // x-go-name: override the derived Go type name
	Validate []string // x-validate: named custom rules
}

// Property is one entry of a schema's properties, in document order.
type Property struct {
	Name     string
	Schema   *Schema
	Required bool
	Pos      pos
}

// Discriminator selects a variant of a oneOf/anyOf by a property value.
type Discriminator struct {
	PropertyName string
	// Mapping maps a discriminator value to a schema, in document order.
	Mapping []DiscriminatorEntry
}

// DiscriminatorEntry is one discriminator value and the schema it selects.
type DiscriminatorEntry struct {
	Value  string
	Schema *Schema
}

// Named reports whether the schema was authored as a component under
// components/schemas, which is the condition for generating a Go named type.
func (s *Schema) Named() bool {
	return s != nil && s.Loc.IsComponentSchema()
}

// IsComponentSchema reports whether the pointer addresses a top-level entry of
// components/schemas rather than some deeper node.
func (l Location) IsComponentSchema() bool {
	if l.File == "" || !strings.HasPrefix(l.Pointer, "/components/schemas/") {
		return false
	}
	return !strings.Contains(l.Pointer[len("/components/schemas/"):], "/")
}

// Is reports whether the schema declares the given JSON Schema type.
func (s *Schema) Is(t string) bool {
	if s == nil {
		return false
	}
	for _, have := range s.Type {
		if have == t {
			return true
		}
	}
	return false
}

// SingleType returns the sole declared type, or "" if the schema declares none
// or more than one.
func (s *Schema) SingleType() string {
	if s == nil || len(s.Type) != 1 {
		return ""
	}
	return s.Type[0]
}

// Prop looks up a property by its wire name.
func (s *Schema) Prop(name string) *Property {
	if s == nil {
		return nil
	}
	for _, p := range s.Properties {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// IsEnum reports whether the schema is a closed set of string values, the case
// that generates a Go named string type with constants.
func (s *Schema) IsEnum() bool {
	return s != nil && len(s.Enum) > 0
}

// Wrapped returns the one schema s wraps, when s is an inline allOf, oneOf or
// anyOf of a single member that adds nothing but annotations: a title,
// description, default, example, nullability or deprecation. Otherwise it
// returns nil.
//
// It is the OpenAPI 3.0 idiom for describing a $ref, whose siblings that
// version ignores, and such a schema means its member. A union of one is the
// same thing, and what anyOf [X, {type: 'null'}] becomes once its null member
// has made it nullable. A named component is never a wrapper: it was given a
// name to be a type of its own.
func (s *Schema) Wrapped() *Schema {
	if s == nil || s.Named() {
		return nil
	}
	var only *Schema
	switch n := [3]int{len(s.AllOf), len(s.OneOf), len(s.AnyOf)}; n {
	case [3]int{1, 0, 0}:
		only = s.AllOf[0]
	case [3]int{0, 1, 0}:
		only = s.OneOf[0]
	case [3]int{0, 0, 1}:
		only = s.AnyOf[0]
	default:
		return nil
	}
	// A discriminator over one member has nothing to choose between.
	adds := len(s.Type) > 0 || s.Format != "" || len(s.Enum) > 0 ||
		s.HasConst || len(s.Properties) > 0 || len(s.Required) > 0 ||
		s.AdditionalPro != nil || s.NoAdditional ||
		s.MinProperties != nil || s.MaxProperties != nil ||
		s.Items != nil || s.MinItems != nil || s.MaxItems != nil ||
		s.UniqueItems || s.MinLength != nil || s.MaxLength != nil ||
		s.Pattern != "" || s.Minimum != nil || s.Maximum != nil ||
		s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil ||
		s.MultipleOf != nil || s.Not != nil || s.GoType != "" ||
		s.GoName != "" || len(s.Validate) > 0
	if adds {
		return nil
	}
	return only
}

// Empty reports whether the schema constrains nothing at all, which maps to
// an untyped Go value.
func (s *Schema) Empty() bool {
	if s == nil {
		return true
	}
	return len(s.Type) == 0 && len(s.Enum) == 0 && !s.HasConst &&
		len(s.Properties) == 0 && s.Items == nil && s.GoType == "" &&
		len(s.AllOf) == 0 && len(s.OneOf) == 0 && len(s.AnyOf) == 0
}

// Walk calls fn for s and, unless fn returns false, every schema it contains.
// Each schema is visited at most once, so cycles terminate.
func (s *Schema) Walk(fn func(*Schema) bool) {
	seen := map[*Schema]bool{}
	var visit func(*Schema)
	visit = func(s *Schema) {
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		if !fn(s) {
			return
		}
		for _, p := range s.Properties {
			visit(p.Schema)
		}
		visit(s.Items)
		visit(s.AdditionalPro)
		visit(s.Not)
		for _, list := range [][]*Schema{s.AllOf, s.OneOf, s.AnyOf} {
			for _, c := range list {
				visit(c)
			}
		}
		if s.Discriminator != nil {
			for _, e := range s.Discriminator.Mapping {
				visit(e.Schema)
			}
		}
	}
	visit(s)
}

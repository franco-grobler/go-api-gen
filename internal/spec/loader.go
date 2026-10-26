package spec

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader reads YAML documents and resolves them into schemas, caching both.
//
// Schemas are memoized by Location, and the memo entry is installed before the
// schema is populated, so recursive schemas resolve without looping and every
// reference to a component yields the same *Schema pointer.
type Loader struct {
	files     map[string]*yaml.Node
	schemas   map[Location]*Schema
	resolving map[Location]bool
}

// NewLoader returns an empty loader.
func NewLoader() *Loader {
	return &Loader{
		files:     map[string]*yaml.Node{},
		schemas:   map[Location]*Schema{},
		resolving: map[Location]bool{},
	}
}

// Files returns the absolute paths of every document the loader has read,
// sorted.
func (l *Loader) Files() []string {
	out := make([]string, 0, len(l.files))
	for f := range l.files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// File reads and parses the document at an absolute path, caching the result.
func (l *Loader) File(abs string) (*yaml.Node, error) {
	if n, ok := l.files[abs]; ok {
		return n, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	root := unwrap(&doc)
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, fmt.Errorf("%s: document is empty", abs)
		}
		root = unwrap(root.Content[0])
	}
	l.files[abs] = root
	return root, nil
}

// nodeAt resolves a Location to the node it addresses.
func (l *Loader) nodeAt(loc Location) (*yaml.Node, error) {
	root, err := l.File(loc.File)
	if err != nil {
		return nil, err
	}
	n, err := resolvePointer(root, loc.Pointer)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", loc.File, err)
	}
	return n, nil
}

// Schema resolves the schema at loc, memoized.
func (l *Loader) Schema(loc Location) (*Schema, error) {
	if s, ok := l.schemas[loc]; ok {
		return s, nil
	}
	node, err := l.nodeAt(loc)
	if err != nil {
		return nil, err
	}

	// A component that is nothing but a $ref is an alias: resolve through to
	// the target rather than minting a second Go type for the same shape.
	if target, ok, err := refOf(loc.File, node); err != nil {
		return nil, err
	} else if ok {
		if l.resolving[loc] {
			return nil, errf(
				at(loc.File, node),
				"$ref cycle: %s references itself through aliases",
				loc,
			)
		}
		l.resolving[loc] = true
		defer delete(l.resolving, loc)

		s, err := l.Schema(target)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", loc, err)
		}
		l.schemas[loc] = s
		return s, nil
	}

	s := &Schema{Loc: loc, Pos: at(loc.File, node)}
	l.schemas[loc] = s // install before filling so cycles terminate
	if err := l.fill(loc.File, node, s); err != nil {
		delete(l.schemas, loc)
		return nil, err
	}
	return s, nil
}

// SchemaNode resolves a schema node that appeared inline in file.
func (l *Loader) SchemaNode(file string, n *yaml.Node) (*Schema, error) {
	if n == nil {
		return nil, nil
	}
	if target, ok, err := refOf(file, n); err != nil {
		return nil, err
	} else if ok {
		return l.Schema(target)
	}
	s := &Schema{Pos: at(file, n)}
	if err := l.fill(file, n, s); err != nil {
		return nil, err
	}
	return s, nil
}

// refSiblings are keywords permitted alongside $ref. They are annotations
// only: they document the reference site and are preserved when bundling, but
// they never alter the referenced schema, which is shared by every referrer.
var refSiblings = map[string]bool{
	"description": true, "title": true, "summary": true,
	"deprecated": true, "example": true, "examples": true,
}

// refOf reports whether n is a reference, and to where.
func refOf(file string, n *yaml.Node) (Location, bool, error) {
	n = unwrap(n)
	if !isMap(n) {
		return Location{}, false, nil
	}
	refNode := mapGet(n, "$ref")
	if refNode == nil {
		return Location{}, false, nil
	}
	ref, err := strVal(file, refNode)
	if err != nil {
		return Location{}, false, err
	}

	var extra []string
	// n is known to be a mapping, so this cannot fail.
	if err := mapEach(n, func(key string, _, _ *yaml.Node) error {
		known := refSiblings[key] || strings.HasPrefix(key, "x-")
		if key != "$ref" && !known {
			extra = append(extra, key)
		}
		return nil
	}); err != nil {
		return Location{}, false, errf(at(file, n), "%v", err)
	}
	if len(extra) > 0 {
		return Location{}, false, errf(at(file, n),
			"$ref cannot be combined with %s; wrap the reference in allOf to "+
				"add constraints",
			strings.Join(quoteAll(extra), ", "))
	}

	loc, err := parseRef(file, ref)
	if err != nil {
		return Location{}, false, errf(at(file, refNode), "%v", err)
	}
	return loc, true, nil
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// ignoredKeywords are accepted and carried into the bundled document but do
// not affect Go code generation.
var ignoredKeywords = map[string]bool{
	"$schema": true, "$id": true, "$comment": true, "$anchor": true,
	"externalDocs": true, "xml": true, "examples": true,
	"readOnly": true, "writeOnly": true,
	"contentMediaType": true, "contentEncoding": true,
}

// unsupportedKeywords are valid JSON Schema but out of scope for v1. Failing
// loudly is better than generating code that quietly ignores a constraint the
// author believed was being enforced.
var unsupportedKeywords = map[string]string{
	"if":                    "conditional schemas",
	"then":                  "conditional schemas",
	"else":                  "conditional schemas",
	"patternProperties":     "patternProperties",
	"propertyNames":         "propertyNames",
	"dependentSchemas":      "dependentSchemas",
	"dependentRequired":     "dependentRequired",
	"prefixItems":           "tuple validation",
	"unevaluatedProperties": "unevaluatedProperties",
	"unevaluatedItems":      "unevaluatedItems",
	"contains":              "contains",
}

// fill parses every supported keyword of a schema node into s.
func (l *Loader) fill(file string, n *yaml.Node, s *Schema) error {
	n = unwrap(n)
	if n == nil {
		return nil
	}
	// A bare boolean schema: true accepts anything, false accepts nothing.
	if n.Kind == yaml.ScalarNode {
		b, err := boolVal(file, n)
		if err != nil {
			return errf(at(file, n), "a schema must be a mapping or a boolean")
		}
		if !b {
			s.Not = &Schema{Pos: at(file, n)}
		}
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return errf(
			at(file, n),
			"a schema must be a mapping, found %s",
			kindName(n),
		)
	}

	var required []string
	var unknown []string

	err := mapEach(n, func(key string, keyNode, v *yaml.Node) error {
		var err error
		switch key {
		case "type":
			var types []string
			if types, err = strsVal(file, v); err == nil {
				for _, t := range types {
					if t == "null" {
						s.Nullable = true
						continue
					}
					s.Type = append(s.Type, t)
				}
			}
		case "nullable": // OpenAPI 3.0 spelling, accepted for migration
			var b bool
			if b, err = boolVal(file, v); err == nil && b {
				s.Nullable = true
			}
		case "format":
			s.Format, err = strVal(file, v)
		case "title":
			s.Title, err = strVal(file, v)
		case "description":
			s.Description, err = strVal(file, v)
		case "deprecated":
			s.Deprecated, err = boolVal(file, v)
		case "default":
			s.Default, err = anyVal(v)
			s.HasDefault = err == nil
		case "example":
			s.Example, err = anyVal(v)
			s.HasExample = err == nil
		case "const":
			s.Const, err = anyVal(v)
			s.HasConst = err == nil
		case "enum":
			err = seqEach(v, func(_ int, e *yaml.Node) error {
				val, err := anyVal(e)
				if err != nil {
					return err
				}
				s.Enum = append(s.Enum, val)
				return nil
			})

		case "properties":
			err = mapEach(v, func(name string, nameNode, ps *yaml.Node) error {
				sub, err := l.SchemaNode(file, ps)
				if err != nil {
					return err
				}
				s.Properties = append(s.Properties, &Property{
					Name: name, Schema: sub, Pos: at(file, nameNode),
				})
				return nil
			})
		case "required":
			required, err = strsVal(file, v)
		case "additionalProperties":
			if v.Kind == yaml.ScalarNode {
				var b bool
				if b, err = boolVal(file, v); err == nil {
					s.NoAdditional = !b
				}
			} else {
				s.AdditionalPro, err = l.SchemaNode(file, v)
			}
		case "minProperties":
			s.MinProperties, err = uintVal(file, v)
		case "maxProperties":
			s.MaxProperties, err = uintVal(file, v)

		case "items":
			s.Items, err = l.SchemaNode(file, v)
		case "minItems":
			s.MinItems, err = uintVal(file, v)
		case "maxItems":
			s.MaxItems, err = uintVal(file, v)
		case "uniqueItems":
			s.UniqueItems, err = boolVal(file, v)

		case "minLength":
			s.MinLength, err = uintVal(file, v)
		case "maxLength":
			s.MaxLength, err = uintVal(file, v)
		case "pattern":
			s.Pattern, err = strVal(file, v)

		case "minimum":
			s.Minimum, err = numVal(file, v)
		case "maximum":
			s.Maximum, err = numVal(file, v)
		case "multipleOf":
			s.MultipleOf, err = numVal(file, v)
		case "exclusiveMinimum":
			s.ExclusiveMinimum, err = exclusiveBound(file, v, &s.Minimum)
		case "exclusiveMaximum":
			s.ExclusiveMaximum, err = exclusiveBound(file, v, &s.Maximum)

		case "allOf":
			s.AllOf, err = l.schemaList(file, v)
		case "oneOf":
			s.OneOf, err = l.schemaList(file, v)
		case "anyOf":
			s.AnyOf, err = l.schemaList(file, v)
		case "not":
			s.Not, err = l.SchemaNode(file, v)
		case "discriminator":
			s.Discriminator, err = l.discriminator(file, v)

		case "x-go-type":
			s.GoType, err = strVal(file, v)
		case "x-go-import":
			s.GoImport, err = strVal(file, v)
		case "x-go-name":
			s.GoName, err = strVal(file, v)
		case "x-validate":
			s.Validate, err = strsVal(file, v)

		default:
			if why, bad := unsupportedKeywords[key]; bad {
				return errf(at(file, keyNode), "%s is not supported; express "+
					"the rule with x-validate instead", why)
			}
			if !ignoredKeywords[key] && !strings.HasPrefix(key, "x-") {
				unknown = append(unknown, key)
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(unknown) > 0 {
		return errf(
			at(file, n),
			"unknown schema keyword(s) %s",
			strings.Join(quoteAll(unknown), ", "),
		)
	}

	s.Required = required
	// Denormalize required onto each property so emitters never have to search
	// the parent's list.
	for _, name := range required {
		if p := s.Prop(name); p != nil {
			p.Required = true
		}
	}
	// A {type: 'null'} member is how JSON Schema makes a union nullable, and
	// it means exactly that: the value may also be null. Dropping it leaves
	// the members a Go type is made from, and a union of one, which is then a
	// wrapper (see Schema.Wrapped).
	s.OneOf = dropNull(s, s.OneOf)
	s.AnyOf = dropNull(s, s.AnyOf)
	s.AllOf = dropEmpty(s, s.AllOf)
	return nil
}

// dropEmpty removes the allOf members that constrain nothing, carrying a
// member's nullable onto the schema. allOf [X, {nullable: true}] is how some
// OpenAPI 3.0 documents make a reference nullable, and it means a nullable X.
func dropEmpty(s *Schema, members []*Schema) []*Schema {
	out := members[:0]
	for _, m := range members {
		if !m.Named() && m.Empty() && len(m.Validate) == 0 && m.Not == nil {
			s.Nullable = s.Nullable || m.Nullable
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dropNull removes the members of a union that accept only null, and marks
// the union nullable when there were any.
func dropNull(s *Schema, members []*Schema) []*Schema {
	out := members[:0]
	for _, m := range members {
		if m.Nullable && m.Empty() && !m.Named() {
			s.Nullable = true
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// exclusiveBound reads exclusiveMinimum/exclusiveMaximum in either spelling:
// OpenAPI 3.1 uses a number, OpenAPI 3.0 used a boolean modifying the
// corresponding inclusive bound.
func exclusiveBound(
	file string,
	v *yaml.Node,
	inclusive **float64,
) (*float64, error) {
	if b, err := boolVal(file, v); err == nil {
		if !b || *inclusive == nil {
			return nil, nil
		}
		bound := *inclusive
		*inclusive = nil // the 3.0 form converts the inclusive bound in place
		return bound, nil
	}
	return numVal(file, v)
}

func (l *Loader) schemaList(file string, v *yaml.Node) ([]*Schema, error) {
	var out []*Schema
	err := seqEach(v, func(_ int, e *yaml.Node) error {
		sub, err := l.SchemaNode(file, e)
		if err != nil {
			return err
		}
		out = append(out, sub)
		return nil
	})
	return out, err
}

func (l *Loader) discriminator(
	file string,
	v *yaml.Node,
) (*Discriminator, error) {
	name, err := strVal(file, mapGet(v, "propertyName"))
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errf(at(file, v), "discriminator: propertyName is required")
	}
	d := &Discriminator{PropertyName: name}
	err = mapEach(mapGet(
		v,
		"mapping",
	), func(value string, _, ref *yaml.Node) error {
		refStr, err := strVal(file, ref)
		if err != nil {
			return err
		}
		loc, err := parseRef(file, refStr)
		if err != nil {
			return errf(
				at(file, ref),
				"discriminator mapping %q: %v",
				value,
				err,
			)
		}
		target, err := l.Schema(loc)
		if err != nil {
			return err
		}
		d.Mapping = append(
			d.Mapping,
			DiscriminatorEntry{Value: value, Schema: target},
		)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

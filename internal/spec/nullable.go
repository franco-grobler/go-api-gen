package spec

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The JSON Schema keywords the translation reads and writes, and the name of
// the type it adds.
const (
	kwType     = "type"
	kwEnum     = "enum"
	kwNullable = "nullable"
	kwAllOf    = "allOf"
	kwOneOf    = "oneOf"
	kwAnyOf    = "anyOf"
	kwNot      = "not"
	kwConst    = "const"
	kwDefault  = "default"
	typeNull   = "null"
)

// legacyOpenAPI reports whether an openapi version is 3.0, whose schemas say
// nullable: true where JSON Schema, and so OpenAPI 3.1, adds a "null" type.
func legacyOpenAPI(v string) bool {
	return v == "3.0" || strings.HasPrefix(v, "3.0.")
}

// legacySchemas finds every schema node a unified document copies that was
// written in OpenAPI 3.0, so rewrite can translate its nullability into the
// JSON Schema of the 3.1 document it lands in. In 3.1, nullable is not a
// keyword at all, so a client generated from a copy left as written refuses
// the null the service accepts.
//
// A schema is 3.0 when its file declares openapi 3.0. A file that declares no
// version, a fragment holding a schema or a path item, is written in the
// version of the document that references it. A fragment reached from both a
// 3.0 and a 3.1 document is translated: it has one copy in the unified
// document, the 3.0 document relies on its nullable, and the loader reads
// nullable the same way whichever version the document declares.
//
// The walk follows the document's structure rather than looking for a
// nullable key anywhere, because not every mapping is a schema: a property
// may be named nullable, and so may a key of an example.
//
// The loader has already resolved every reference walked here, so the walk
// passes over one it cannot follow; rewrite would report it, with its
// position.
func (l *Loader) legacySchemas(
	sources []UnifySource,
	comps []BundleComponent,
) map[*yaml.Node]bool {
	w := &dialectWalk{
		loader: l,
		legacy: map[*yaml.Node]bool{},
		seen:   map[walkKey]bool{},
	}
	for _, src := range sources {
		file := src.Doc.File
		root, err := l.File(file)
		if err != nil {
			continue
		}
		legacy := w.dialect(file, false)
		for _, item := range members(mapGet(root, "paths")) {
			w.pathItem(file, item, legacy)
		}
	}
	// Every component is reached from some path. Should one not be, it is
	// still in the version its own file declares.
	for _, c := range comps {
		n, err := l.nodeAt(c.Schema.Loc)
		if err != nil {
			continue
		}
		file := c.Schema.Loc.File
		w.schema(file, n, w.dialect(file, false))
	}
	return w.legacy
}

// members returns the values of a mapping or the elements of a sequence, and
// nothing for any other node.
func members(n *yaml.Node) []*yaml.Node {
	n = unwrap(n)
	switch {
	case n == nil:
		return nil
	case n.Kind == yaml.SequenceNode:
		return n.Content
	case n.Kind == yaml.MappingNode:
		out := make([]*yaml.Node, 0, len(n.Content)/2)
		for i := 1; i < len(n.Content); i += 2 {
			out = append(out, n.Content[i])
		}
		return out
	default:
		return nil
	}
}

// dialectWalk visits the schemas of OpenAPI documents, noting which are 3.0.
type dialectWalk struct {
	loader *Loader
	legacy map[*yaml.Node]bool
	seen   map[walkKey]bool
}

// walkKey is a node visited in one dialect. A fragment can be reached in
// both, and only a visit as 3.0 marks it.
type walkKey struct {
	n      *yaml.Node
	legacy bool
}

// dialect reports whether file is written in OpenAPI 3.0: as its own openapi
// declares, or as inherited from the document that reached it when it
// declares none.
func (w *dialectWalk) dialect(file string, inherited bool) bool {
	root, err := w.loader.File(file)
	if err != nil {
		return inherited
	}
	v := mapGet(root, "openapi")
	if v == nil {
		return inherited
	}
	return legacyOpenAPI(v.Value)
}

// follow resolves a reference written in file, returning the node it names,
// the file that holds it, and that file's dialect.
func (w *dialectWalk) follow(
	file, ref string,
	legacy bool,
) (target *yaml.Node, in string, inLegacy, ok bool) {
	loc, err := parseRef(file, ref)
	if err != nil {
		return nil, "", false, false
	}
	if target, err = w.loader.nodeAt(loc); err != nil {
		return nil, "", false, false
	}
	return target, loc.File, w.dialect(loc.File, legacy), true
}

// enter resolves n through any chain of references to the object it stands
// for, and reports false when that is not a mapping or was already walked in
// this dialect.
func (w *dialectWalk) enter(
	file string,
	n *yaml.Node,
	legacy bool,
) (in string, obj *yaml.Node, inLegacy, ok bool) {
	for {
		n = unwrap(n)
		if !isMap(n) || w.seen[walkKey{n, legacy}] {
			return file, n, legacy, false
		}
		w.seen[walkKey{n, legacy}] = true
		ref := mapGet(n, "$ref")
		if ref == nil {
			return file, n, legacy, true
		}
		if n, file, legacy, ok = w.follow(file, ref.Value, legacy); !ok {
			return file, n, legacy, false
		}
	}
}

func (w *dialectWalk) pathItem(file string, n *yaml.Node, legacy bool) {
	file, n, legacy, ok := w.enter(file, n, legacy)
	if !ok {
		return
	}
	w.params(file, mapGet(n, "parameters"), legacy)
	for i := 0; i+1 < len(n.Content); i += 2 {
		if isMethod(strings.ToLower(unwrap(n.Content[i]).Value)) {
			w.operation(file, n.Content[i+1], legacy)
		}
	}
}

func (w *dialectWalk) operation(file string, n *yaml.Node, legacy bool) {
	w.params(file, mapGet(n, "parameters"), legacy)
	if file, body, legacy, ok := w.enter(
		file,
		mapGet(n, "requestBody"),
		legacy,
	); ok {
		w.content(file, mapGet(body, "content"), legacy)
	}
	for _, r := range members(mapGet(n, "responses")) {
		if file, r, legacy, ok := w.enter(file, r, legacy); ok {
			w.headers(file, mapGet(r, "headers"), legacy)
			w.content(file, mapGet(r, "content"), legacy)
		}
	}
	for _, cb := range members(mapGet(n, "callbacks")) {
		if file, cb, legacy, ok := w.enter(file, cb, legacy); ok {
			for _, item := range members(cb) {
				w.pathItem(file, item, legacy)
			}
		}
	}
}

func (w *dialectWalk) params(file string, n *yaml.Node, legacy bool) {
	for _, p := range members(n) {
		w.param(file, p, legacy)
	}
}

func (w *dialectWalk) headers(file string, n *yaml.Node, legacy bool) {
	for _, h := range members(n) {
		w.param(file, h, legacy)
	}
}

// param walks a parameter or a header, which hold a schema the same way.
func (w *dialectWalk) param(file string, n *yaml.Node, legacy bool) {
	file, n, legacy, ok := w.enter(file, n, legacy)
	if !ok {
		return
	}
	w.schema(file, mapGet(n, "schema"), legacy)
	w.content(file, mapGet(n, "content"), legacy)
}

func (w *dialectWalk) content(file string, n *yaml.Node, legacy bool) {
	for _, media := range members(n) {
		w.schema(file, mapGet(media, "schema"), legacy)
		for _, enc := range members(mapGet(media, "encoding")) {
			w.headers(file, mapGet(enc, "headers"), legacy)
		}
	}
}

// schema marks a schema written in 3.0 and walks every schema it holds: the
// same keywords the loader reads subschemas from.
func (w *dialectWalk) schema(file string, n *yaml.Node, legacy bool) {
	file, n, legacy, ok := w.enter(file, n, legacy)
	if !ok {
		return
	}
	if legacy {
		w.legacy[n] = true
	}
	for _, p := range members(mapGet(n, "properties")) {
		w.schema(file, p, legacy)
	}
	for _, key := range []string{"items", "additionalProperties", kwNot} {
		w.schema(file, mapGet(n, key), legacy)
	}
	for _, key := range []string{kwAllOf, kwOneOf, kwAnyOf} {
		for _, m := range members(mapGet(n, key)) {
			w.schema(file, m, legacy)
		}
	}
	mapping := mapGet(mapGet(n, "discriminator"), "mapping")
	for _, ref := range members(mapping) {
		if target, in, inLegacy, ok := w.follow(
			file,
			ref.Value,
			legacy,
		); ok {
			w.schema(in, target, inLegacy)
		}
	}
}

// annotationKeywords describe a schema without constraining it. When a
// nullable schema has to become anyOf [the schema, {type: 'null'}], these stay
// on the outside, where a docs renderer and a client generator read them, and
// where they leave it a wrapper to the loader (see Schema.Wrapped).
var annotationKeywords = map[string]bool{
	"title": true, "description": true, "deprecated": true,
	kwDefault: true, "example": true, "examples": true,
	"externalDocs": true, "readOnly": true, "writeOnly": true,
}

// nullableTo31 translates the nullability of one OpenAPI 3.0 schema into
// JSON Schema, returning a new mapping that shares every node it does not
// change with n, which is left as it is. Subschemas are not translated here:
// rewrite reaches each of them in turn.
//
// nullable: false is the default and is dropped. nullable: true becomes the
// form the loader reads back as nullable:
//
//   - with type, enum, oneOf or anyOf, null joins each of them: type gains
//     "null", enum gains null, and oneOf and anyOf gain a {type: 'null'}
//     member. JSON Schema checks each keyword on its own, so a null missing
//     from any one of them refuses it;
//   - otherwise, and whenever allOf, not or const would refuse null whatever
//     the rest allows, the schema becomes anyOf [the schema, {type: 'null'}],
//     its annotations staying outside. An allOf of one member is the 3.0
//     spelling of that member, so the member stands in the anyOf directly.
//
// An allOf member that holds nothing but nullable and annotations is the 3.0
// idiom allOf [X, {nullable: true}] for a nullable X. The loader drops such a
// member and makes the schema nullable (see dropEmpty), and so does this.
func nullableTo31(n *yaml.Node) *yaml.Node {
	if mapGet(n, kwNullable) == nil && !hasNullableMember(n) {
		return n
	}
	s := &yaml.Node{Kind: yaml.MappingNode, Tag: n.Tag, Style: n.Style}
	nullable := false
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch unwrap(k).Value {
		case kwNullable:
			nullable = isTrue(v)
		case kwAllOf:
			var annotations []*yaml.Node
			var on bool
			v, annotations, on = dropNullableMembers(v)
			nullable = nullable || on
			if v != nil {
				s.Content = append(s.Content, k, v)
			}
			// The dropped members' annotations apply to the schema, where
			// the schema does not say otherwise.
			for j := 0; j+1 < len(annotations); j += 2 {
				key := unwrap(annotations[j]).Value
				if mapGet(n, key) == nil && mapGet(s, key) == nil {
					s.Content = append(s.Content, annotations[j:j+2]...)
				}
			}
		default:
			s.Content = append(s.Content, k, v)
		}
	}
	if !nullable {
		return s
	}

	carries, refuses := false, false
	for _, key := range []string{kwType, kwEnum, kwOneOf, kwAnyOf} {
		carries = carries || mapGet(s, key) != nil
	}
	for _, key := range []string{kwAllOf, kwNot, kwConst} {
		refuses = refuses || mapGet(s, key) != nil
	}
	if carries && !refuses {
		for i := 0; i+1 < len(s.Content); i += 2 {
			v := s.Content[i+1]
			switch unwrap(s.Content[i]).Value {
			case kwType:
				s.Content[i+1] = withNullType(v)
			case kwEnum:
				s.Content[i+1] = withNullValue(v)
			case kwOneOf, kwAnyOf:
				s.Content[i+1] = withNullMember(v)
			}
		}
		return s
	}

	outer := &yaml.Node{Kind: yaml.MappingNode, Tag: n.Tag, Style: n.Style}
	inner := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	for i := 0; i+1 < len(s.Content); i += 2 {
		if annotationKeywords[unwrap(s.Content[i]).Value] {
			outer.Content = append(outer.Content, s.Content[i:i+2]...)
		} else {
			inner.Content = append(inner.Content, s.Content[i:i+2]...)
		}
	}
	member := inner
	if all := mapGet(inner, kwAllOf); len(inner.Content) == 2 &&
		all != nil && all.Kind == yaml.SequenceNode && len(all.Content) == 1 {
		member = all.Content[0]
	}
	anyOf := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq}
	anyOf.Content = []*yaml.Node{member, nullSchema()}
	mapSet(outer, kwAnyOf, anyOf)
	return outer
}

// hasNullableMember reports whether an allOf of n has a member that holds
// nothing but nullable and annotations.
func hasNullableMember(n *yaml.Node) bool {
	for _, m := range members(mapGet(n, kwAllOf)) {
		if nullableOnly(m) {
			return true
		}
	}
	return false
}

// nullableOnly reports whether a schema holds nothing but nullable and
// annotations.
func nullableOnly(m *yaml.Node) bool {
	m = unwrap(m)
	if !isMap(m) || mapGet(m, kwNullable) == nil {
		return false
	}
	for i := 0; i < len(m.Content); i += 2 {
		key := unwrap(m.Content[i]).Value
		if key != kwNullable && !annotationKeywords[key] {
			return false
		}
	}
	return true
}

// dropNullableMembers returns an allOf without its members that hold nothing
// but nullable and annotations, or nil when no member is left, along with
// those members' annotations and whether any of them said nullable: true.
func dropNullableMembers(
	all *yaml.Node,
) (rest *yaml.Node, annotations []*yaml.Node, nullable bool) {
	all = unwrap(all)
	if all == nil || all.Kind != yaml.SequenceNode {
		return all, nil, false
	}
	rest = &yaml.Node{Kind: yaml.SequenceNode, Tag: all.Tag, Style: all.Style}
	for _, m := range all.Content {
		if !nullableOnly(m) {
			rest.Content = append(rest.Content, m)
			continue
		}
		m = unwrap(m)
		for i := 0; i+1 < len(m.Content); i += 2 {
			if unwrap(m.Content[i]).Value == kwNullable {
				nullable = nullable || isTrue(m.Content[i+1])
				continue
			}
			annotations = append(annotations, m.Content[i:i+2]...)
		}
	}
	if len(rest.Content) == 0 {
		return nil, annotations, nullable
	}
	return rest, annotations, nullable
}

// isTrue reads a boolean as the loader does.
func isTrue(n *yaml.Node) bool {
	b, err := strconv.ParseBool(unwrap(n).Value)
	return err == nil && b
}

// withNullType adds "null" to a type, a single name becoming a list.
func withNullType(t *yaml.Node) *yaml.Node {
	t = unwrap(t)
	out := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq,
		Style: yaml.FlowStyle}
	switch t.Kind {
	case yaml.ScalarNode:
		if t.Value == typeNull {
			return t
		}
		out.Content = []*yaml.Node{t}
	case yaml.SequenceNode:
		for _, c := range t.Content {
			if unwrap(c).Value == typeNull {
				return t
			}
		}
		out.Style = t.Style
		out.Content = append(out.Content, t.Content...)
	default:
		return t
	}
	out.Content = append(out.Content, scalar(typeNull))
	return out
}

// withNullValue adds null to an enum's values.
func withNullValue(enum *yaml.Node) *yaml.Node {
	enum = unwrap(enum)
	if enum.Kind != yaml.SequenceNode {
		return enum
	}
	for _, c := range enum.Content {
		if unwrap(c).ShortTag() == tagNull {
			return enum
		}
	}
	out := &yaml.Node{Kind: yaml.SequenceNode, Tag: enum.Tag,
		Style: enum.Style}
	out.Content = append(out.Content, enum.Content...)
	out.Content = append(out.Content, &yaml.Node{
		Kind: yaml.ScalarNode, Tag: tagNull, Value: typeNull,
	})
	return out
}

// withNullMember adds a {type: 'null'} member to a oneOf or anyOf, which is
// how JSON Schema makes a union nullable and how the loader reads one back
// (see dropNull).
func withNullMember(union *yaml.Node) *yaml.Node {
	union = unwrap(union)
	if union.Kind != yaml.SequenceNode {
		return union
	}
	for _, m := range union.Content {
		if t := mapGet(m, kwType); t != nil && t.Value == typeNull &&
			len(unwrap(m).Content) == 2 {
			return union
		}
	}
	out := &yaml.Node{Kind: yaml.SequenceNode, Tag: union.Tag,
		Style: union.Style}
	out.Content = append(out.Content, union.Content...)
	out.Content = append(out.Content, nullSchema())
	return out
}

// nullSchema is {type: 'null'}, the schema that accepts only null.
func nullSchema() *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, Style: yaml.FlowStyle}
	mapSet(n, kwType, scalar(typeNull))
	return n
}

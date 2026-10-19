package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/franco-grobler/go-api-gen/internal/gomap"
	"github.com/franco-grobler/go-api-gen/internal/naming"
	"github.com/franco-grobler/go-api-gen/internal/spec"
	"github.com/franco-grobler/go-api-gen/internal/workspace"
)

// OptionalImport is the runtime package providing Optional[T].
const OptionalImport = "github.com/franco-grobler/go-api-gen/runtime/optional"

// kind classifies how a named schema is declared in Go.
type kind int

const (
	kindStruct kind = iota // object with properties, or an allOf composition
	kindEnum               // a closed set of values
	kindArray              // a named slice
	kindMap                // an object constrained only by additionalProperties
	kindScalar             // a named string, integer, number, or boolean
	kindAny                // no constraints the Go type system can express
)

// typeEmitter decides the Go type names for one package and declares them.
//
// One instance is shared by the types, validation, and server emitters, so an
// inline schema gets the same synthesized name wherever it is referenced, and
// every synthesized type is declared exactly once.
type typeEmitter struct {
	ws     *workspace.Workspace
	mapper *gomap.Mapper
	svc    *workspace.Service // nil when emitting a shared root

	dedupe *naming.Deduper
	synth  map[*spec.Schema]string // inline schema -> synthesized type name
	queue  []queued
	done   map[*spec.Schema]bool

	// declared is every type this package declares, named and synthesized, in
	// declaration order. Validation walks it so a synthesized type gets a
	// Validate method alongside the named ones.
	declared []queued
}

type queued struct {
	schema *spec.Schema
	name   string
}

// newTypeEmitter returns the naming authority for one package's types.
func (g *Generator) newTypeEmitter(
	svc *workspace.Service, owned []*spec.Schema,
) *typeEmitter {
	e := &typeEmitter{
		ws:     g.ws,
		mapper: g.mapper,
		svc:    svc,
		dedupe: naming.NewDeduper(),
		synth:  map[*spec.Schema]string{},
		done:   map[*spec.Schema]bool{},
	}
	// Reserve the names the workspace already assigned so a synthesized type
	// can never collide with a declared one.
	for _, s := range owned {
		e.dedupe.Take(g.ws.GoName(s))
	}
	for _, s := range owned {
		e.queue = append(e.queue, queued{s, g.ws.GoName(s)})
	}
	return e
}

// EmitTypes declares every queued type into f.
//
// Schemas owned by another package are referenced through an import, never
// redeclared, which is the entire point of the shared-root model.
func (e *typeEmitter) EmitTypes(f *File) error {
	// A declaration can queue more work, so drain rather than range.
	for len(e.queue) > 0 {
		next := e.queue[0]
		e.queue = e.queue[1:]
		if e.done[next.schema] {
			continue
		}
		e.done[next.schema] = true
		e.declared = append(e.declared, next)
		if err := e.declare(f, next.schema, next.name); err != nil {
			return err
		}
	}
	return nil
}

// classify decides how a schema is declared.
func classify(s *spec.Schema) kind {
	switch {
	case s.IsEnum():
		return kindEnum
	case len(s.OneOf) > 0 || len(s.AnyOf) > 0:
		return kindStruct // rejected in declare with a specific message
	case len(s.Properties) > 0 || len(s.AllOf) > 0:
		return kindStruct
	case s.Is("array"):
		return kindArray
	case s.Is("object") && s.AdditionalPro != nil:
		return kindMap
	case s.Is("object"):
		return kindMap // an object with no declared properties is a free map
	case s.SingleType() != "":
		return kindScalar
	default:
		return kindAny
	}
}

// aliasesScalar reports whether a named scalar mapped to t is declared as an
// alias. A defined type over an imported struct such as time.Time would lose
// its methods and its JSON encoding, so those become aliases.
func aliasesScalar(t gomap.Type) bool {
	return t.Import != "" || strings.HasPrefix(t.Expr, "[]")
}

// aliased reports whether a named schema is declared as an alias rather than
// a defined type.
//
// An alias has no method set of its own: a Validate method on an alias of
// uuid.UUID is a method on uuid.UUID, which Go refuses outside that package.
// Validation therefore checks an aliased schema wherever it is used, never
// through a method.
func aliased(m *gomap.Mapper, s *spec.Schema) bool {
	switch classify(s) {
	case kindScalar:
		t, _ := m.Scalar(s)
		return aliasesScalar(t)
	case kindAny:
		return true
	default:
		return false
	}
}

// nameOf returns the Go type a schema is declared as, from the package being
// emitted: its workspace name when it has one, the name synthesized for it
// when it was declared inline, and "" when it is not a declared type at all.
func (e *typeEmitter) nameOf(s *spec.Schema) string {
	if name := e.ws.Qualify(s, e.svc); name != "" {
		return name
	}
	return e.synth[s]
}

func (e *typeEmitter) declare(f *File, s *spec.Schema, name string) error {
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return fmt.Errorf("%s: schema %q uses oneOf/anyOf, which is not "+
			"supported yet.\n"+
			"Model the variants as separate schemas, or set x-go-type to a "+
			"hand-written Go type.",
			s.Loc, name)
	}

	e.doc(f, s, name)
	switch classify(s) {
	case kindEnum:
		return e.declareEnum(f, s, name)
	case kindStruct:
		return e.declareStruct(f, s, name)
	case kindArray:
		elem, err := e.typeOf(f, s.Items, name, "Item")
		if err != nil {
			return err
		}
		f.P("type %s []%s\n", name, elem)
	case kindMap:
		val := "any"
		if s.AdditionalPro != nil && !s.AdditionalPro.Empty() {
			var err error
			val, err = e.typeOf(f, s.AdditionalPro, name, "Value")
			if err != nil {
				return err
			}
		}
		f.P("type %s map[string]%s\n", name, val)
	case kindScalar:
		t, _ := e.mapper.Scalar(s)
		expr := f.ImportType(t.Expr, t.Import)
		if aliasesScalar(t) {
			f.P("type %s = %s\n", name, expr)
		} else {
			f.P("type %s %s\n", name, expr)
		}
	default:
		f.P("type %s = any\n", name)
	}
	return nil
}

func (e *typeEmitter) doc(f *File, s *spec.Schema, name string) {
	desc := s.Description
	if desc == "" {
		desc = s.Title
	}
	if desc == "" {
		return
	}
	// Lead with the identifier so the comment reads as Go doc.
	first, rest, _ := strings.Cut(desc, "\n")
	f.Comment(name + " " + lowerFirstWord(first))
	if rest != "" {
		f.Comment(rest)
	}
	if s.Deprecated {
		f.Comment("")
		f.Comment("Deprecated: this schema is marked deprecated in the " +
			"API specification.")
	}
}

// lowerFirstWord makes a description read naturally after the type name,
// unless it starts with an acronym or a proper noun.
func lowerFirstWord(s string) string {
	if s == "" {
		return ""
	}
	word, _, _ := strings.Cut(s, " ")
	if strings.ToUpper(word) == word && len(word) > 1 {
		return s // an acronym keeps its case
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func (e *typeEmitter) declareEnum(f *File, s *spec.Schema, name string) error {
	base := "string"
	if t, ok := e.mapper.Scalar(s); ok && t.Import == "" {
		base = t.Expr
	}
	f.P("type %s %s\n", name, base)

	f.P("// Valid values of %s.", name)
	f.P("const (")
	consts := naming.NewDeduper()
	for _, v := range s.Enum {
		lit, str, ok := enumLiteral(v, base)
		if !ok {
			return fmt.Errorf(
				"%s: enum value %v of %q is not representable as %s",
				s.Loc,
				v,
				name,
				base,
			)
		}
		f.P(
			"\t%s %s = %s",
			consts.Take(naming.EnumConst(name, str)),
			name,
			lit,
		)
	}
	f.P(")\n")

	// A generated set makes validation a lookup rather than a chain of
	// comparisons, and gives callers a way to enumerate the values.
	f.P("// Valid reports whether v is a defined value of %s.", name)
	f.P("func (v %s) Valid() bool {", name)
	f.P("\tswitch v {")
	var lits []string
	for _, v := range s.Enum {
		lit, _, _ := enumLiteral(v, base)
		lits = append(lits, lit)
	}
	f.P("\tcase %s:", strings.Join(lits, ", "))
	f.P("\t\treturn true")
	f.P("\t}")
	f.P("\treturn false")
	f.P("}\n")
	return nil
}

// enumLiteral renders an enum value as a Go literal and as the string used to
// build its constant name.
func enumLiteral(v any, base string) (lit, str string, ok bool) {
	switch t := v.(type) {
	case string:
		if base != "string" {
			return "", "", false
		}
		return strconv.Quote(t), t, true
	case int:
		return strconv.Itoa(t), strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), strconv.FormatInt(t, 10), true
	case float64:
		s := strconv.FormatFloat(t, 'g', -1, 64)
		return s, s, true
	case bool:
		s := strconv.FormatBool(t)
		return s, s, true
	default:
		return "", "", false
	}
}

func (e *typeEmitter) declareStruct(
	f *File, s *spec.Schema, name string,
) error {
	items := structItems(e.ws, s)

	f.P("type %s struct {", name)
	for _, it := range items {
		if it.embed != nil {
			expr, err := e.reference(f, it.embed)
			if err != nil {
				return err
			}
			f.P("\t%s", expr)
			continue
		}
		if err := e.structField(f, it.prop, it.name, name); err != nil {
			return err
		}
	}

	required := requiredNames(s)
	var decoders []string
	for _, it := range items {
		if it.embed != nil && hasDecoder(it.embed) {
			decoders = append(decoders, it.name)
		}
	}
	if len(required) > 0 {
		f.P("")
		f.P("\t// missingFields records the required properties absent")
		f.P("\t// from the JSON that produced this value, so validation")
		f.P("\t// can report them by name rather than guessing from a")
		f.P("\t// zero value that may have been sent deliberately.")
		f.P("\tmissingFields []string `json:\"-\"`")
	}
	f.P("}\n")

	if len(required) > 0 || len(decoders) > 0 {
		e.unmarshalJSON(f, name, required, decoders)
	}
	return nil
}

// hasDecoder reports whether the struct declared from a named schema has an
// UnmarshalJSON of its own: one with required properties to track, or one
// embedding a member that has one, since that method would otherwise be
// promoted onto it.
func hasDecoder(s *spec.Schema) bool {
	if classify(s) != kindStruct {
		return false
	}
	if len(requiredNames(s)) > 0 {
		return true
	}
	for _, member := range s.AllOf {
		if member.Named() && hasDecoder(member) {
			return true
		}
	}
	return false
}

// structItem is one entry of a struct declared from a schema: a named allOf
// member it embeds, or a property it declares as a field of its own.
type structItem struct {
	embed *spec.Schema   // the embedded member; nil for a field
	prop  *spec.Property // the property; nil for an embedded member
	name  string         // the field's Go name; an embedded type's own name
}

// structItems lays out the struct declared from s, in declaration order.
//
// An allOf member that is itself a named schema is embedded, which keeps one
// Go type for one schema instead of copying its fields. An inline member has
// no type of its own, so its properties become fields of this struct, ahead
// of the schema's own. Field names are deduplicated across all of them.
//
// The type and validation emitters both read this layout, so a field is
// checked under exactly the name it was declared with.
func structItems(ws *workspace.Workspace, s *spec.Schema) []structItem {
	fields := naming.NewDeduper()
	var out []structItem
	declare := func(from *spec.Schema) {
		for _, p := range from.Properties {
			out = append(out, structItem{
				prop: p,
				name: fields.Take(naming.Exported(p.Name)),
			})
		}
	}
	for _, member := range s.AllOf {
		if !member.Named() {
			declare(member)
			continue
		}
		// An embedded field is named for its type, unqualified.
		name := fields.Take(ws.GoName(member))
		out = append(out, structItem{embed: member, name: name})
	}
	declare(s)
	return out
}

// requiredNames lists the wire names of a schema's required properties,
// including those contributed by an inline allOf member.
func requiredNames(s *spec.Schema) []string {
	var out []string
	seen := map[string]bool{}
	add := func(from *spec.Schema) {
		for _, p := range from.Properties {
			if p.Required && !seen[p.Name] {
				seen[p.Name] = true
				out = append(out, p.Name)
			}
		}
	}
	for _, member := range s.AllOf {
		if !member.Named() {
			add(member)
		}
	}
	add(s)
	return out
}

// unmarshalJSON emits a decoder that records which required keys were absent.
//
// Go cannot otherwise tell a field the client omitted from one it sent as the
// zero value, and reporting a deliberate 0 or "" as missing would be wrong.
//
// decoders names the embedded members that have a decoder of their own. Each
// is called in turn so it records its own absent keys, which its Validate
// reports.
func (e *typeEmitter) unmarshalJSON(
	f *File, name string, required, decoders []string,
) {
	jsonPkg := f.Import("encoding/json")
	recv := "x"

	f.Comment("UnmarshalJSON decodes " + name +
		" and records which required properties were absent.")
	f.P("func (%s *%s) UnmarshalJSON(data []byte) error {", recv, name)
	if len(decoders) == 0 {
		f.P("\t// The alias drops this method, so decoding does not recurse.")
		f.P("\ttype alias %s", name)
		f.P("\tvar decoded alias")
		f.P("\tif err := %s.Unmarshal(data, &decoded); err != nil {",
			jsonPkg)
		f.P("\t\treturn err")
		f.P("\t}")
		f.P("\t*%s = %s(decoded)\n", recv, name)
	} else {
		// The alias drops this method but not one promoted from an embedded
		// member, which would decode the whole body into that member alone
		// and leave every other field at its zero value. A field of the same
		// name, shallower than the promoted method, shadows it, so the
		// wrapper is decoded field by field, the embedded ones included.
		f.P("\ttype alias %s", name)
		f.P("\tvar decoded struct {")
		f.P("\t\talias")
		f.P("\t\tUnmarshalJSON struct{} `json:\"-\"`")
		f.P("\t}")
		f.P("\tif err := %s.Unmarshal(data, &decoded); err != nil {",
			jsonPkg)
		f.P("\t\treturn err")
		f.P("\t}")
		f.P("\t*%s = %s(decoded.alias)\n", recv, name)
		for _, d := range decoders {
			f.P("\tif err := %s.%s.UnmarshalJSON(data); err != nil {",
				recv, d)
			f.P("\t\treturn err")
			f.P("\t}")
		}
		if len(required) == 0 {
			f.P("\treturn nil")
			f.P("}\n")
			return
		}
		f.P("")
	}
	f.P("\tvar present map[string]%s.RawMessage", jsonPkg)
	f.P("\tif err := %s.Unmarshal(data, &present); err != nil {", jsonPkg)
	f.P("\t\treturn err")
	f.P("\t}")
	f.P("\tfor _, name := range []string{")
	for _, r := range required {
		f.P("\t\t%s,", strconv.Quote(r))
	}
	f.P("\t} {")
	f.P("\t\tif _, ok := present[name]; !ok {")
	f.P("\t\t\t%s.missingFields = append(%s.missingFields, name)",
		recv, recv)
	f.P("\t\t}")
	f.P("\t}")
	f.P("\treturn nil")
	f.P("}\n")
}

// structField declares one property as a field named goName.
func (e *typeEmitter) structField(
	f *File,
	p *spec.Property,
	goName string,
	owner string,
) error {
	expr, err := e.typeOf(f, p.Schema, owner, naming.Exported(p.Name))
	if err != nil {
		return fmt.Errorf("property %q: %w", p.Name, err)
	}

	// A field is a plain Go type only when it is always present and can
	// never be null. Everything else is Optional, so a handler can tell
	// "absent" from "explicitly cleared".
	tag := strconv.Quote(p.Name)
	if !p.Required || p.Schema.Nullable {
		expr = fmt.Sprintf(
			"%s.Optional[%s]",
			f.Import(OptionalImport),
			expr,
		)
		tag = strconv.Quote(p.Name + ",omitzero")
	}

	if desc := p.Schema.Description; desc != "" && !p.Schema.Named() {
		f.P("\t// %s %s", goName, lowerFirstWord(firstLine(desc)))
	}
	if p.Schema.Deprecated {
		f.P("\t// Deprecated: this property is marked deprecated in " +
			"the API specification.")
	}
	f.P("\t%s %s `json:%s`", goName, expr, tag)
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func lastSegment(expr string) string {
	if i := strings.LastIndex(expr, "."); i >= 0 {
		return expr[i+1:]
	}
	return expr
}

// typeOf returns the Go type expression for a schema used at a given position.
// An inline object or enum is given a synthesized name derived from where it
// appears, and queued for declaration.
func (e *typeEmitter) typeOf(
	f *File,
	s *spec.Schema,
	owner,
	role string,
) (string, error) {
	if s == nil || s.Empty() {
		return "any", nil
	}
	if s.Named() {
		return e.reference(f, s)
	}
	if s.GoType != "" {
		return f.ImportType(s.GoType, s.GoImport), nil
	}

	// A schema already given a name keeps it: asking twice, once while
	// planning and once while emitting, must not mint a second type.
	if name, ok := e.synth[s]; ok {
		return name, nil
	}

	switch classify(s) {
	case kindEnum, kindStruct:
		name := e.dedupe.Take(owner + role)
		e.synth[s] = name
		e.queue = append(e.queue, queued{s, name})
		return name, nil
	case kindArray:
		elem, err := e.typeOf(f, s.Items, owner, role+"Item")
		if err != nil {
			return "", err
		}
		return "[]" + elem, nil
	case kindMap:
		if s.AdditionalPro == nil || s.AdditionalPro.Empty() {
			return "map[string]any", nil
		}
		val, err := e.typeOf(f, s.AdditionalPro, owner, role+"Value")
		if err != nil {
			return "", err
		}
		return "map[string]" + val, nil
	case kindScalar:
		t, _ := e.mapper.Scalar(s)
		return f.ImportType(t.Expr, t.Import), nil
	default:
		return "any", nil
	}
}

// reference returns how to name a schema that is declared elsewhere: bare when
// this package owns it, package-qualified when another does.
func (e *typeEmitter) reference(f *File, s *spec.Schema) (string, error) {
	owner := e.ws.Owner(s)
	if owner == nil {
		return "", fmt.Errorf(
			"%s: schema %q has no owning package",
			s.Loc,
			s.Loc.ComponentName(),
		)
	}
	name := e.ws.GoName(s)
	if owner.Service != nil && owner.Service == e.svc {
		return name, nil
	}
	ownRoot := owner.Root != nil && owner.Root.GoPackage == f.Package
	if e.svc == nil && ownRoot {
		return name, nil
	}
	f.ImportAs(owner.ImportPath(), owner.GoPackage())
	return owner.GoPackage() + "." + name, nil
}

package gen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/franco-grobler/go-api-gen/internal/gomap"
	"github.com/franco-grobler/go-api-gen/internal/naming"
	"github.com/franco-grobler/go-api-gen/internal/spec"
	"github.com/franco-grobler/go-api-gen/internal/workspace"
)

// UnionImport is the runtime package that chooses a union's variant.
const UnionImport = "github.com/franco-grobler/go-api-gen/runtime/union"

// jsonString is the JSON Schema string type.
const jsonString = "string"

// variant is one member of a oneOf or anyOf, as the union's struct holds it.
type variant struct {
	schema *spec.Schema // the member, unwrapped
	field  string       // the struct field that holds it
	label  string       // how a message names it
	tags   []string     // the discriminator values that select it
}

// unionMembers returns the members of a union and how it treats a value that
// fits more than one: "OneOf" or "AnyOf", the runtime's names for the two.
func unionMembers(s *spec.Schema) (members []*spec.Schema, mode string) {
	if len(s.OneOf) > 0 {
		return s.OneOf, "OneOf"
	}
	return s.AnyOf, "AnyOf"
}

// checkUnion rejects the unions this generator does not model yet, each with
// what to do instead.
func checkUnion(s *spec.Schema, name string) error {
	switch {
	case len(s.OneOf) > 0 && len(s.AnyOf) > 0:
		return fmt.Errorf("%s: schema %q has both oneOf and anyOf, which is "+
			"not supported yet.\n"+
			"Nest one inside the other as a member, or set x-go-type to a "+
			"hand-written Go type.", s.Pos, name)
	case len(s.Properties) > 0 || len(s.AllOf) > 0:
		return fmt.Errorf("%s: schema %q combines oneOf/anyOf with "+
			"properties or allOf, which is not supported yet.\n"+
			"Move the shared properties into each variant, or set x-go-type "+
			"to a hand-written Go type.", s.Pos, name)
	}
	return nil
}

// unionVariants lays out the struct declared from a union: one field per
// member, named for the member's type, and the discriminator values that
// select each one when the union has a discriminator.
//
// The type and validation emitters both read this layout, so a variant is
// checked under exactly the field it was declared as.
func unionVariants(ws *workspace.Workspace, s *spec.Schema,
	name string,
) ([]variant, error) {
	members, _ := unionMembers(s)
	fields := naming.NewDeduper()
	out := make([]variant, 0, len(members))
	for _, m := range members {
		m = unwrap(m)
		v := variant{schema: m}
		switch {
		case m.Named():
			v.field = fields.Take(ws.GoName(m))
			v.label = m.Loc.ComponentName()
		case m.Title != "":
			v.field = fields.Take(naming.Exported(m.Title))
			v.label = m.Title
		default:
			v.field = fields.Take(kindWord(m))
			v.label = jsonType(m)
		}
		out = append(out, v)
	}
	d := s.Discriminator
	if d == nil {
		return out, nil
	}
	for _, e := range d.Mapping {
		found := false
		for i := range out {
			if out[i].schema == unwrap(e.Schema) {
				out[i].tags = append(out[i].tags, e.Value)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s: schema %q: discriminator value %q "+
				"maps to a schema that is not one of its members",
				s.Pos, name, e.Value)
		}
	}
	for i := range out {
		if len(out[i].tags) > 0 {
			continue
		}
		// The specification's default is the member's schema name. Many
		// documents, OpenAI's among them, instead rely on the value the
		// member's own discriminator property is fixed to, so that is
		// preferred when there is one.
		if fixed := fixedValues(objectProperties(out[i].schema),
			d.PropertyName); len(fixed) == 1 {
			var tag string
			if json.Unmarshal([]byte(fixed[0]), &tag) == nil {
				out[i].tags = []string{tag}
				continue
			}
		}
		if !out[i].schema.Named() {
			return nil, fmt.Errorf("%s: schema %q: an inline member has no "+
				"discriminator value.\n"+
				"Add it to discriminator.mapping, or give the member's %q "+
				"property a single enum value.",
				s.Pos, name, d.PropertyName)
		}
		out[i].tags = []string{out[i].schema.Loc.ComponentName()}
	}
	return out, nil
}

// kindWord names an inline member's field for the kind of value it holds.
func kindWord(s *spec.Schema) string {
	switch classify(s) {
	case kindUnion:
		return "Union"
	case kindStruct, kindMap:
		return "Object"
	case kindArray:
		item := unwrap(s.Items)
		if item.Named() {
			return item.Loc.ComponentName() + "List"
		}
		return kindWord(item) + "List"
	case kindEnum:
		return naming.Exported(jsonType(s)) + "Enum"
	case kindAny:
		return "Value"
	}
	return naming.Exported(jsonType(s))
}

// jsonType names the JSON type a schema accepts, for a message.
func jsonType(s *spec.Schema) string {
	if s == nil {
		return "value"
	}
	if t := s.SingleType(); t != "" {
		return t
	}
	switch classify(s) {
	case kindStruct, kindMap:
		return "object"
	case kindArray:
		return "array"
	case kindEnum:
		return jsonString
	}
	return "value"
}

// objectProperties gathers the properties an object accepts: its own, and
// those of every allOf member, inline or named, since a JSON value has them
// all at one level whichever schema declared them.
func objectProperties(s *spec.Schema) []*spec.Property {
	var out []*spec.Property
	seen := map[*spec.Schema]bool{}
	var walk func(*spec.Schema)
	walk = func(s *spec.Schema) {
		s = unwrap(s)
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		for _, m := range s.AllOf {
			walk(m)
		}
		out = append(out, s.Properties...)
	}
	walk(s)
	return out
}

// fixedValues returns, as compact JSON, the values the named property is
// restricted to by an enum or a const, or nil when it is not restricted.
func fixedValues(props []*spec.Property, name string) []string {
	for _, p := range props {
		if p.Name == name {
			return allowedValues(unwrap(p.Schema))
		}
	}
	return nil
}

// allowedValues returns the values an enum or a const allows, as compact
// JSON, or nil when the schema allows any value of its type.
func allowedValues(s *spec.Schema) []string {
	if s == nil {
		return nil
	}
	vals := s.Enum
	if s.HasConst {
		vals = []any{s.Const}
	}
	var out []string
	for _, v := range vals {
		if b, err := json.Marshal(v); err == nil {
			out = append(out, string(b))
		}
	}
	return out
}

// shapeKinds returns the union.Kind constants for the JSON kinds a member
// accepts.
func shapeKinds(m *gomap.Mapper, s *spec.Schema) []string {
	if s.GoType == "" {
		switch classify(s) {
		case kindStruct, kindMap:
			return []string{"Object"}
		case kindArray:
			return []string{"Array"}
		case kindUnion, kindAny:
			return []string{"Any"}
		}
	}
	var out []string
	for _, t := range s.Type {
		switch t {
		case "object":
			out = append(out, "Object")
		case "array":
			out = append(out, "Array")
		case jsonString:
			out = append(out, "String")
		case "integer":
			out = append(out, "Integer")
		case "number":
			out = append(out, "Number")
		case "boolean":
			out = append(out, "Boolean")
		}
	}
	if len(out) == 0 && s.IsEnum() {
		// An untyped enum accepts whatever its values are.
		if t, ok := m.Scalar(s); ok && t.Expr != jsonString {
			return []string{"Any"}
		}
		return []string{"String"}
	}
	if len(out) == 0 {
		return []string{"Any"}
	}
	return out
}

// shapeLiteral renders the union.Shape that tells a member apart from the
// others.
func shapeLiteral(m *gomap.Mapper, unionPkg string, v *variant) string {
	s := v.schema
	var b strings.Builder
	fmt.Fprintf(&b, "{\n\t\tName: %q,\n", v.label)
	kinds := shapeKinds(m, s)
	for i := range kinds {
		kinds[i] = unionPkg + "." + kinds[i]
	}
	fmt.Fprintf(&b, "\t\tKinds: %s,\n", strings.Join(kinds, " | "))
	if vals := allowedValues(s); len(vals) > 0 {
		fmt.Fprintf(&b, "\t\tValues: %s,\n", stringSlice(vals))
	}
	if kinds[0] == unionPkg+".Object" && s.GoType == "" {
		props := objectProperties(s)
		var names, required []string
		var fixed []string
		for _, p := range props {
			names = append(names, p.Name)
			if p.Required {
				required = append(required, p.Name)
			}
			if vals := allowedValues(unwrap(p.Schema)); len(vals) > 0 {
				fixed = append(fixed, fmt.Sprintf(
					"\t\t\t{Property: %q, Values: %s},\n",
					p.Name, stringSlice(vals)))
			}
		}
		if len(required) > 0 {
			fmt.Fprintf(&b, "\t\tRequired: %s,\n", stringSlice(required))
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "\t\tProperties: %s,\n", stringSlice(names))
		}
		if s.NoAdditional {
			b.WriteString("\t\tClosed: true,\n")
		}
		if len(fixed) > 0 {
			fmt.Fprintf(&b, "\t\tFixed: []%s.Fixed{\n%s\t\t},\n",
				unionPkg, strings.Join(fixed, ""))
		}
	}
	b.WriteString("\t}")
	return b.String()
}

func stringSlice(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = strconv.Quote(v)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// declareUnion declares a oneOf or anyOf as a struct with one pointer field
// per member, exactly one of which is set, and the JSON methods that decode a
// value into the member it is and encode whichever member is set.
func (e *typeEmitter) declareUnion(f *File, s *spec.Schema, name string) error {
	if err := checkUnion(s, name); err != nil {
		return err
	}
	variants, err := unionVariants(e.ws, s, name)
	if err != nil {
		return err
	}
	_, mode := unionMembers(s)
	unionPkg := f.Import(UnionImport)
	jsonPkg := f.Import("encoding/json")

	labels := make([]string, len(variants))
	for i, v := range variants {
		labels[i] = v.field
	}
	if s.Description != "" || s.Title != "" {
		f.Comment("")
	}
	f.Comment(name + " is one of " + orList(labels) + ": exactly one " +
		"field is set.")
	f.P("type %s struct {", name)
	types := make([]string, len(variants))
	for i, v := range variants {
		if types[i], err = e.typeOf(f, v.schema, name, v.field); err != nil {
			return fmt.Errorf("%s: %w", v.label, err)
		}
		f.P("\t%s *%s", v.field, types[i])
	}
	f.P("")
	f.P("\t// problem is why the JSON this value was decoded from is none of")
	f.P("\t// the variants, which Validate reports.")
	f.P("\tproblem *%s.Problem", unionPkg)
	f.P("}\n")

	recv := "x"
	f.Comment("UnmarshalJSON decodes " + name +
		" into the variant the JSON is.")
	f.P("func (%s *%s) UnmarshalJSON(data []byte) error {", recv, name)
	f.P("\t*%s = %s{}", recv, name)
	if s.Nullable {
		f.P("\tif %s.IsNull(data) {", unionPkg)
		f.P("\t\treturn nil")
		f.P("\t}")
	}
	decode := func(indent string, i int) {
		f.P("%s%s.%s = new(%s)", indent, recv, variants[i].field, types[i])
		f.P("%sreturn %s.Unmarshal(data, %s.%s)", indent, jsonPkg, recv,
			variants[i].field)
	}
	if d := s.Discriminator; d != nil && distinctTags(variants) {
		f.P("\ttag, problem := %s.Tag(data, %q)", unionPkg, d.PropertyName)
		f.P("\tif problem != nil {")
		f.P("\t\t%s.problem = problem", recv)
		f.P("\t\treturn nil")
		f.P("\t}")
		f.P("\tswitch tag {")
		var all []string
		for i, v := range variants {
			quoted := make([]string, len(v.tags))
			for i, t := range v.tags {
				quoted[i] = strconv.Quote(t)
			}
			all = append(all, v.tags...)
			f.P("\tcase %s:", strings.Join(quoted, ", "))
			decode("\t\t", i)
		}
		f.P("\t}")
		sort.Strings(all)
		f.P("\t%s.problem = %s.UnknownTag(tag, %q, %s)", recv, unionPkg,
			d.PropertyName, stringSlice(all))
		f.P("\treturn nil")
		f.P("}\n")
	} else {
		shapes := naming.Unexported(name) + "Shapes"
		f.P("\tswitch i, problem := %s.Choose(data, %s.%s, %s); i {",
			unionPkg, unionPkg, mode, shapes)
		for i := range variants {
			f.P("\tcase %d:", i)
			decode("\t\t", i)
		}
		f.P("\tdefault:")
		f.P("\t\t%s.problem = problem", recv)
		f.P("\t\treturn nil")
		f.P("\t}")
		f.P("}\n")

		f.Comment(shapes + " tells the variants of " + name + " apart, " +
			"in declaration order.")
		f.P("var %s = []%s.Shape{", shapes, unionPkg)
		for i := range variants {
			f.P("\t%s,", shapeLiteral(e.mapper, unionPkg, &variants[i]))
		}
		f.P("}\n")
	}

	f.Comment("MarshalJSON encodes the variant that is set.")
	f.P("func (%s %s) MarshalJSON() ([]byte, error) {", recv, name)
	f.P("\tswitch {")
	for _, v := range variants {
		f.P("\tcase %s.%s != nil:", recv, v.field)
		f.P("\t\treturn %s.Marshal(%s.%s)", jsonPkg, recv, v.field)
	}
	f.P("\t}")
	if s.Nullable {
		f.P("\treturn []byte(\"null\"), nil")
	} else {
		f.P("\treturn nil, %s.New(%q)", f.Import("errors"),
			f.Package+"."+name+": no variant is set")
	}
	f.P("}\n")
	return nil
}

// distinctTags reports whether every discriminator value selects one
// variant. When two variants share one, as OpenAI's input and output
// messages share "message", the discriminator cannot choose between them,
// and the union is decoded by shape instead, which tells them apart by their
// other keys.
func distinctTags(variants []variant) bool {
	seen := map[string]bool{}
	for _, v := range variants {
		for _, t := range v.tags {
			if seen[t] {
				return false
			}
			seen[t] = true
		}
	}
	return true
}

// orList joins names as "A", "A or B", or "A, B, or C".
func orList(names []string) string {
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " +
		names[len(names)-1]
}

// unionBody renders the checks for a union being validated through expr:
// why it matched no variant, when it did not, and otherwise the checks of the
// variant that is set.
func (e *validateEmitter) unionBody(b *strings.Builder, s *spec.Schema,
	expr, v string,
) {
	fmt.Fprintf(b, "\tif p := %s.problem; p != nil {\n", expr)
	fmt.Fprintf(b, "\t\t%s.Fail(p.Type, p.Msg, p.Ctx...)\n", v)
	fmt.Fprintf(b, "\t}\n")
	variants, err := unionVariants(e.ws, s, "")
	if err != nil {
		return // reported when the type was declared
	}
	for _, vr := range variants {
		if !needsCheck(e.mapper, vr.schema) {
			continue
		}
		field := expr + "." + vr.field
		fmt.Fprintf(b, "\tif %s != nil {\n", field)
		e.value(b, vr.schema, "(*"+field+")", v, "\t\t")
		fmt.Fprintf(b, "\t}\n")
	}
}

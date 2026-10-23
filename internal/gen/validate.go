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

// Runtime packages the generated code imports.
const (
	ValidateImport = "github.com/franco-grobler/go-api-gen/runtime/validate"
	APIErrImport   = "github.com/franco-grobler/go-api-gen/runtime/apierr"
	HTTPXImport    = "github.com/franco-grobler/go-api-gen/runtime/httpx"
)

// validateEmitter renders the Validate methods for one package's schemas.
type validateEmitter struct {
	ws     *workspace.Workspace
	mapper *gomap.Mapper
	file   *File
	svc    *workspace.Service

	validatePkg string   // qualifier for the validate runtime package
	patterns    []string // regexp vars to declare, in emission order
	patternName map[string]string
}

// EmitValidate renders validation for the given schemas.
//
// Constraints become plain Go code rather than tags interpreted at runtime, so
// what runs is what you can read, step through, and grep for.
func (g *Generator) EmitValidate(
	file *File, svc *workspace.Service, declared []queued,
) error {
	e := &validateEmitter{
		ws:          g.ws,
		mapper:      g.mapper,
		file:        file,
		svc:         svc,
		patternName: map[string]string{},
	}
	e.validatePkg = file.Import(ValidateImport)

	var body strings.Builder
	saved := file.SwapBuffer(&body)

	for _, d := range declared {
		if err := e.schema(d.schema, d.name); err != nil {
			return err
		}
	}
	file.RestoreBuffer(saved)

	// Pattern variables are collected while walking, so they are declared
	// first and the methods that use them follow.
	if len(e.patterns) > 0 {
		regexpPkg := file.Import("regexp")
		file.P("// Compiled once at init rather than on every request.")
		file.P("var (")
		for _, p := range e.patterns {
			file.P("\t%s = %s.MustCompile(%s)",
				e.patternName[p], regexpPkg, backquote(p))
		}
		file.P(")\n")
	}
	file.WriteRaw(body.String())
	return nil
}

// schema emits the Validate method, and any x-validate interface, for one
// named schema.
func (e *validateEmitter) schema(s *spec.Schema, name string) error {
	if aliased(e.mapper, s) {
		// An alias cannot carry a method, so its constraints are checked by
		// value wherever it is used. A custom rule has nowhere to live.
		if len(s.Validate) > 0 {
			return fmt.Errorf("%s: x-validate on %q cannot be honoured: %s "+
				"is declared as an alias, and an alias has no methods of "+
				"its own.\n"+
				"Move the rule to the property that uses %s.",
				s.Loc, name, name, name)
		}
		return nil
	}

	recv := naming.Unexported(name)
	if recv == name {
		recv = "x"
	}

	// Custom rules are declared as an interface the type must satisfy, so a
	// rule named in the spec but never written is a compile error rather than
	// a silently skipped check.
	rules := collectRules(s)
	if len(rules) > 0 {
		e.file.Comment(name + "Validator is the set of custom rules " +
			"declared with x-validate on " + name + ".")
		e.file.Comment("Implement each method in a file this generator " +
			"does not own.")
		e.file.P("type %sValidator interface {", name)
		for _, r := range rules {
			e.file.P("\t%s(v *%s.V)", r.method, e.validatePkg)
		}
		e.file.P("}\n")
		e.file.P("// Compile-time proof that every declared rule is written.")
		e.file.P("var _ %sValidator = %s{}\n", name, name)
	}

	e.file.Comment("Validate checks " + name +
		" against the constraints in the specification.")
	e.file.P("func (%s %s) Validate(v *%s.V) {", recv, name, e.validatePkg)

	body := e.schemaBody(s, recv, "v")
	if strings.TrimSpace(body) == "" {
		e.file.P("\t_ = %s", recv)
		e.file.P("\t_ = v")
	} else {
		e.file.WriteRaw(body)
	}
	e.file.P("}\n")
	return nil
}

// rule is one x-validate entry.
type rule struct {
	method string
	loc    string // the sub-location it validates, empty for the whole schema
}

// collectRules gathers the schema-level and property-level custom rules,
// including those of an inline allOf member, whose rules and properties are
// this type's own.
func collectRules(s *spec.Schema) []rule {
	var out []rule
	seen := map[string]bool{}
	add := func(r rule) {
		// One rule reached twice, say on a member and on the schema, is one
		// method to write and one check to run.
		if !seen[r.method] {
			seen[r.method] = true
			out = append(out, r)
		}
	}
	own := []*spec.Schema{}
	for _, member := range s.AllOf {
		if !member.Named() {
			own = append(own, member)
		}
	}
	own = append(own, s)

	for _, from := range own {
		for _, name := range from.Validate {
			add(rule{method: "Validate" + naming.Exported(name)})
		}
	}
	for _, from := range own {
		for _, p := range from.Properties {
			// A property rule is named for the property it guards, so two
			// schemas can both declare "reserved_words" without colliding.
			for _, name := range p.Schema.Validate {
				if p.Schema.Named() {
					// The rule belongs to the referenced type, not this one.
					continue
				}
				add(rule{
					method: "Validate" + naming.Exported(p.Name) +
						naming.Exported(name),
					loc: p.Name,
				})
			}
		}
	}
	return out
}

// schemaBody renders the checks for a schema being validated through expr.
func (e *validateEmitter) schemaBody(s *spec.Schema, expr, v string) string {
	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, "\t"+format+"\n", args...)
	}

	switch {
	case len(s.Properties) > 0 || len(s.AllOf) > 0:
		// Fields the client never sent are reported before anything else, so
		// the response leads with what is missing rather than what is empty.
		if hasRequired(s) {
			w("for _, name := range %s.missingFields {", expr)
			w("\t%s.Field(name).Fail(\"missing\", \"Field required\")", v)
			w("}")
		}
		lo, hi := propertyBounds(s)
		e.propertyCount(&b, lo, hi, expr+".propertyCount", v, "\t")
		// Walked in the layout the struct was declared with, so a property
		// an inline allOf member contributed is checked like one of the
		// schema's own, under the field name it was given.
		for _, it := range structItems(e.ws, s) {
			switch {
			case it.prop != nil:
				e.property(&b, it.prop, it.name, expr, v)
			case !aliased(e.mapper, it.embed):
				w("%s.%s.Validate(%s)", expr, it.name, v)
			}
		}
	case s.IsEnum():
		w("if !%s.Valid() {", expr)
		w("\t%s.Fail(\"enum\", \"Input should be one of %s\")",
			v, strings.Join(enumValueList(s), ", "))
		w("}")
		e.scalarChecks(&b, s, expr, v, "\t")
	default:
		e.scalarChecks(&b, s, expr, v, "\t")
	}

	for _, r := range collectRules(s) {
		target := v
		if r.loc != "" {
			target = fmt.Sprintf("%s.Field(%q)", v, r.loc)
		}
		w("%s.%s(%s)", expr, r.method, target)
	}
	return b.String()
}

// hasRequired reports whether the struct carries a missingFields slice, which
// it does exactly when requiredNames found something to track.
func hasRequired(s *spec.Schema) bool {
	return len(requiredNames(s)) > 0
}

// property renders the checks for one struct field.
func (e *validateEmitter) property(b *strings.Builder, p *spec.Property,
	goName, owner, v string,
) {
	if !needsCheck(e.mapper, p.Schema) {
		return
	}
	field := owner + "." + goName
	loc := fmt.Sprintf("%s.Field(%q)", v, p.Name)
	optional := !p.Required || p.Schema.Nullable

	if optional {
		// An absent or null field has nothing to check; that it may be absent
		// at all was already settled by the schema.
		fmt.Fprintf(b, "\tif val, ok := %s.Get(); ok {\n", field)
		e.value(b, p.Schema, "val", loc, "\t\t")
		fmt.Fprintf(b, "\t}\n")
		return
	}

	// A required field the client never sent has already been reported as
	// missing. Checking it anyway would measure the zero value and report the
	// same field again -- as too short, or below a minimum -- for a value that
	// does not exist.
	fmt.Fprintf(b, "\tif !%s.Absent(%s.missingFields, %q) {\n",
		e.validatePkg, owner, p.Name)
	e.value(b, p.Schema, field, loc, "\t\t")
	fmt.Fprintf(b, "\t}\n")
}

// value renders the checks for one value at a location.
func (e *validateEmitter) value(b *strings.Builder, s *spec.Schema,
	expr, loc, indent string,
) {
	s = unwrap(s)
	switch {
	case s.Named() && !aliased(e.mapper, s):
		// A named schema validates itself, wherever it is used. An alias has
		// no Validate method, so it falls through to the checks for its value.
		fmt.Fprintf(b, "%s%s.Validate(%s)\n", indent, expr, loc)
	case s.GoType != "":
		// A hand-written type is opaque: the keywords beside x-go-type
		// describe its JSON, which the type is responsible for.
	case s.Is("array") && s.Items != nil:
		e.scalarChecks(b, s, expr, loc, indent)
		if needsCheck(e.mapper, s.Items) {
			fmt.Fprintf(b, "%sfor i, item := range %s {\n", indent, expr)
			e.value(b, s.Items, "item",
				fmt.Sprintf("%s.Index(i)", loc), indent+"\t")
			fmt.Fprintf(b, "%s}\n", indent)
		}
	case len(s.Properties) > 0:
		// An inline object was synthesized into its own type, which validates
		// itself the same way a named one does.
		fmt.Fprintf(b, "%s%s.Validate(%s)\n", indent, expr, loc)
	default:
		e.scalarChecks(b, s, expr, loc, indent)
	}
}

// needsCheck reports whether a schema constrains anything worth emitting.
//
// A named schema always does, through its Validate method, unless it is an
// alias: that has no method, so only its own keywords count.
func needsCheck(m *gomap.Mapper, s *spec.Schema) bool {
	s = unwrap(s)
	if s == nil {
		return false
	}
	if s.Named() && !aliased(m, s) {
		return true
	}
	if s.GoType != "" {
		return false // opaque; see value
	}
	// x-validate on an unnamed schema is not counted: its owner calls the
	// rule as a method (see collectRules), so the value itself has nothing
	// to check, and an Optional unwrapped for nothing does not compile.
	if len(s.Properties) > 0 {
		return true
	}
	if s.Is("array") && needsCheck(m, s.Items) {
		return true
	}
	return s.MinLength != nil || s.MaxLength != nil || s.Pattern != "" ||
		s.Minimum != nil || s.Maximum != nil ||
		s.ExclusiveMinimum != nil || s.ExclusiveMaximum != nil ||
		s.MultipleOf != nil || s.MinItems != nil || s.MaxItems != nil ||
		s.MinProperties != nil || s.MaxProperties != nil ||
		len(s.Enum) > 0
}

// propertyCount renders the minProperties and maxProperties checks against
// count, an expression for the number of keys the object holds.
func (e *validateEmitter) propertyCount(b *strings.Builder, lo, hi *uint64,
	count, loc, indent string,
) {
	bounds := []struct {
		n   *uint64
		op  string
		msg string
		key string
	}{
		{lo, "<", "Object should have at least %d propert%s", "min_length"},
		{hi, ">", "Object should have at most %d propert%s", "max_length"},
	}
	slugs := map[string]string{"<": "too_short", ">": "too_long"}
	for _, c := range bounds {
		if c.n == nil {
			continue
		}
		n := *c.n
		suffix := "ies"
		if n == 1 {
			suffix = "y"
		}
		fmt.Fprintf(b, "%sif %s %s %d {\n", indent, count, c.op, n)
		fmt.Fprintf(b, "%s\t%s.Fail(%q, %q, %q, %d)\n", indent, loc,
			slugs[c.op], fmt.Sprintf(c.msg, n, suffix), c.key, n)
		fmt.Fprintf(b, "%s}\n", indent)
	}
}

// scalarChecks renders the constraint keywords that apply to a value directly.
//
// The slugs mirror Pydantic's vocabulary so that a client written against a
// FastAPI service recognises them unchanged.
func (e *validateEmitter) scalarChecks(b *strings.Builder, s *spec.Schema,
	expr, loc, indent string,
) {
	fail := func(slug, msg string, ctx ...string) string {
		args := ""
		if len(ctx) > 0 {
			args = ", " + strings.Join(ctx, ", ")
		}
		return fmt.Sprintf("%s\t%s.Fail(%q, %q%s)\n",
			indent, loc, slug, msg, args)
	}
	cond := func(condition, body string) {
		fmt.Fprintf(b, "%sif %s {\n%s%s}\n", indent, condition, body, indent)
	}

	if s.MinLength != nil {
		n := *s.MinLength
		utf8Pkg := e.file.Import("unicode/utf8")
		cond(fmt.Sprintf("%s.RuneCountInString(string(%s)) < %d",
			utf8Pkg, expr, n),
			fail("string_too_short",
				fmt.Sprintf("String should have at least %d character%s",
					n, plural(n)),
				"\"min_length\"", strconv.FormatUint(n, 10)))
	}
	if s.MaxLength != nil {
		n := *s.MaxLength
		utf8Pkg := e.file.Import("unicode/utf8")
		cond(fmt.Sprintf("%s.RuneCountInString(string(%s)) > %d",
			utf8Pkg, expr, n),
			fail("string_too_long",
				fmt.Sprintf("String should have at most %d character%s",
					n, plural(n)),
				"\"max_length\"", strconv.FormatUint(n, 10)))
	}
	if s.Pattern != "" {
		v := e.pattern(s.Pattern)
		cond(fmt.Sprintf("!%s.MatchString(string(%s))", v, expr),
			fail("string_pattern_mismatch",
				"String should match pattern "+s.Pattern,
				"\"pattern\"", backquote(s.Pattern)))
	}

	numeric := []struct {
		bound *float64
		op    string
		slug  string
		msg   string
		key   string
	}{
		{s.Minimum, "<", "greater_than_equal", "Input should be >= %s", "ge"},
		{s.Maximum, ">", "less_than_equal", "Input should be <= %s", "le"},
		{s.ExclusiveMinimum, "<=", "greater_than",
			"Input should be > %s", "gt"},
		{s.ExclusiveMaximum, ">=", "less_than", "Input should be < %s", "lt"},
	}
	for _, n := range numeric {
		if n.bound == nil {
			continue
		}
		lit := formatNumber(*n.bound)
		cond(fmt.Sprintf("%s %s %s", expr, n.op, lit),
			fail(n.slug, fmt.Sprintf(n.msg, lit),
				strconv.Quote(n.key), lit))
	}

	if s.MinItems != nil {
		n := *s.MinItems
		cond(fmt.Sprintf("len(%s) < %d", expr, n),
			fail("too_short",
				fmt.Sprintf("List should have at least %d item%s",
					n, plural(n)),
				"\"min_length\"", strconv.FormatUint(n, 10)))
	}
	if s.MaxItems != nil {
		n := *s.MaxItems
		cond(fmt.Sprintf("len(%s) > %d", expr, n),
			fail("too_long",
				fmt.Sprintf("List should have at most %d item%s", n, plural(n)),
				"\"max_length\"", strconv.FormatUint(n, 10)))
	}

	// A map holds exactly the keys it was decoded from. A struct cannot be
	// measured this way, so it counts them as it decodes (see schemaBody).
	if s.MinProperties != nil || s.MaxProperties != nil {
		e.propertyCount(b, s.MinProperties, s.MaxProperties,
			"len("+expr+")", loc, indent)
	}

	// An enum on an unnamed value is checked inline; a named enum type has a
	// generated Valid method and is checked through that.
	if len(s.Enum) > 0 && !s.Named() {
		var lits []string
		for _, val := range s.Enum {
			lit, _, ok := enumLiteral(val, "string")
			if !ok {
				continue
			}
			lits = append(lits, lit)
		}
		if len(lits) > 0 {
			cond(fmt.Sprintf("!%s.OneOfString(string(%s), %s)",
				e.validatePkg, expr, strings.Join(lits, ", ")),
				fail("enum",
					"Input should be one of "+strings.Join(lits, ", ")))
		}
	}
}

// enumValueList renders a schema's enum values for a diagnostic message.
func enumValueList(s *spec.Schema) []string {
	out := make([]string, 0, len(s.Enum))
	for _, val := range s.Enum {
		_, str, ok := enumLiteral(val, "string")
		if !ok {
			continue
		}
		out = append(out, str)
	}
	return out
}

func plural(n uint64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// formatNumber renders a bound without a trailing .0 on whole values, so the
// generated comparison reads the way the spec was written.
func formatNumber(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// pattern registers a regular expression and returns its variable name.
func (e *validateEmitter) pattern(expr string) string {
	if name, ok := e.patternName[expr]; ok {
		return name
	}
	name := fmt.Sprintf("pattern%d", len(e.patterns))
	e.patternName[expr] = name
	e.patterns = append(e.patterns, expr)
	return name
}

// backquote renders a string as a Go literal, preferring a raw literal so a
// regular expression's backslashes survive unescaped.
func backquote(s string) string {
	if !strings.ContainsAny(s, "`\n") {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

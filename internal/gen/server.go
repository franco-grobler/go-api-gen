package gen

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/franco-grobler/go-api-gen/internal/naming"
	"github.com/franco-grobler/go-api-gen/internal/spec"
	"github.com/franco-grobler/go-api-gen/internal/workspace"
)

// serverEmitter renders the HTTP layer for one service.
type serverEmitter struct {
	ws   *workspace.Workspace
	gen  *Generator
	file *File
	svc  *workspace.Service

	types *typeEmitter

	validatePkg string
	apierrPkg   string
	schemes     []*spec.SecurityScheme
	httpxPkg    string
	httpPkg     string
	ctxPkg      string
	jsonPkg     string
}

// operation is one endpoint, with the Go names derived for it.
type operation struct {
	op       *spec.Operation
	name     string // exported Go name, from operationId
	request  string // request struct name
	success  *spec.Response
	respType string // Go type of the success body, empty when there is none
	multi    bool   // more than one 2xx response
}

// EmitServer renders the routing, decoding, and dispatch for a service.
//
// The generated server owns everything between the socket and the handler:
// route registration, parameter parsing, body decoding, and validation. A
// handler receives a value that has already been checked and returns either a
// result or an error, and every error leaves as one problem+json shape.
func (g *Generator) EmitServer(
	file *File, svc *workspace.Service, types *typeEmitter,
) error {
	e := &serverEmitter{ws: g.ws, gen: g, file: file, svc: svc, types: types}
	e.httpPkg = file.Import("net/http")
	e.ctxPkg = file.Import("context")
	e.apierrPkg = file.Import(APIErrImport)
	e.validatePkg = file.Import(ValidateImport)
	e.httpxPkg = file.Import(HTTPXImport)
	e.schemes = e.usedSchemes()

	ops, err := e.operations()
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}

	e.handlerInterface(ops)
	e.requestTypes(ops)
	e.security(ops)
	e.router(ops)
	for _, op := range ops {
		if err := e.handler(op); err != nil {
			return err
		}
	}
	return nil
}

// operations resolves every operation's Go names and response type.
func (e *serverEmitter) operations() ([]*operation, error) {
	var out []*operation
	names := naming.NewDeduper()

	for _, pi := range e.svc.Doc.Paths {
		for _, op := range pi.Operations {
			name := names.Take(naming.Exported(op.OperationID))
			o := &operation{op: op, name: name, request: name + "Request"}

			success := op.Success()
			switch len(success) {
			case 0:
				return nil, fmt.Errorf(
					"%s: operation %q declares no 2xx response",
					op.Pos, op.OperationID)
			case 1:
				o.success = success[0]
			default:
				o.multi = true
				o.success = success[0]
			}

			if m := o.success.JSON(); m != nil && m.Schema != nil {
				t, err := e.typeOf(m.Schema, name, "Response")
				if err != nil {
					return nil, err
				}
				o.respType = t
			}
			out = append(out, o)
		}
	}
	return out, nil
}

// typeOf resolves a schema to a Go type through the package's shared type
// emitter, so a response body and a struct field name the same type and an
// inline schema is declared exactly once.
func (e *serverEmitter) typeOf(
	s *spec.Schema, owner, role string,
) (string, error) {
	return e.types.typeOf(e.file, s, owner, role)
}

func (e *serverEmitter) handlerInterface(ops []*operation) {
	e.file.Comment("Handler is the service's business logic.")
	e.file.Comment("")
	e.file.Comment("Each method receives a request that has already been " +
		"decoded and validated, so a handler never parses or checks input. " +
		"Return an error to fail: an *apierr.Problem is rendered as it " +
		"stands, and anything else becomes a 500 with its detail withheld.")
	e.file.P("type Handler interface {")
	for _, o := range ops {
		if doc := o.op.Summary; doc != "" {
			e.file.P("\t// %s %s", o.name, lowerFirstWord(firstLine(doc)))
		}
		e.file.P("\t%s(ctx %s.Context, req %s) (%s, error)",
			o.name, e.ctxPkg, o.request, e.resultType(o))
		e.file.P("")
	}
	e.file.P("}\n")
}

// resultType is what a handler returns on success.
func (e *serverEmitter) resultType(o *operation) string {
	if o.respType == "" {
		return "struct{}"
	}
	return "*" + o.respType
}

func (e *serverEmitter) requestTypes(ops []*operation) {
	for _, o := range ops {
		path := o.op.Params("path")
		query := o.op.Params("query")
		header := o.op.Params("header")
		body := o.op.RequestBody.JSON()

		e.file.Comment(o.request + " is the decoded input to " + o.name + ".")
		e.file.P("type %s struct {", o.request)
		if len(path) > 0 {
			e.file.P("\tPath %sPathParams", o.name)
		}
		if len(query) > 0 {
			e.file.P("\tQuery %sQueryParams", o.name)
		}
		if len(header) > 0 {
			e.file.P("\tHeader %sHeaderParams", o.name)
		}
		if body != nil && body.Schema != nil {
			t, err := e.typeOf(body.Schema, o.name, "Body")
			if err == nil {
				e.file.P("\tBody %s", t)
			}
		}
		e.file.P("")
		e.file.P("\t// Raw is the underlying request, for a handler that " +
			"needs the transport:")
		e.file.P("\t// authentication context, the remote address, " +
			"cancellation.")
		e.file.P("\tRaw *%s.Request", e.httpPkg)
		e.file.P("}\n")

		e.paramStruct(o.name+"PathParams", path)
		e.paramStruct(o.name+"QueryParams", query)
		e.paramStruct(o.name+"HeaderParams", header)
	}
}

func (e *serverEmitter) paramStruct(name string, params []*spec.Parameter) {
	if len(params) == 0 {
		return
	}
	e.file.P("// %s holds the parameters decoded for the request.", name)
	e.file.P("type %s struct {", name)
	fields := naming.NewDeduper()
	for _, p := range params {
		goName := fields.Take(naming.Exported(p.Name))
		t, err := e.typeOf(p.Schema, name, goName)
		if err != nil {
			t = "string"
		}
		if !p.Required {
			t = fmt.Sprintf("%s.Optional[%s]",
				e.file.Import(OptionalImport), t)
		}
		if p.Description != "" {
			e.file.P("\t// %s %s", goName,
				lowerFirstWord(firstLine(p.Description)))
		}
		e.file.P("\t%s %s", goName, t)
	}
	e.file.P("}\n")
}

func (e *serverEmitter) router(ops []*operation) {
	e.file.Comment("ServerOption configures the generated server.")
	e.file.Comment("")
	e.file.Comment("The option constructors live in httpx, since they are " +
		"the same for every service; they are aliased here so a caller needs " +
		"only this package.")
	e.file.P("type ServerOption = %s.Option\n", e.httpxPkg)
	e.file.P("var (")
	e.file.P("\t// WithMiddleware wraps every route, outermost first.")
	e.file.P("\tWithMiddleware = %s.WithMiddleware", e.httpxPkg)
	e.file.P("")
	e.file.P("\t// WithErrorOptions configures how problems are rendered.")
	e.file.P("\tWithErrorOptions = %s.WithErrorOptions", e.httpxPkg)
	e.file.P(")\n")

	e.errorOptions()

	e.file.Comment("NewServer returns an http.Handler serving every " +
		"operation in the specification.")
	if e.secured() {
		e.file.Comment("")
		e.file.Comment("The Authenticator is a parameter rather than an " +
			"option because the specification declares security: a server " +
			"built without one would route requests nobody had checked, and " +
			"that is not a mistake worth leaving until runtime.")
	}
	e.file.P("func NewServer(h Handler%s, opts ...ServerOption) %s.Handler {",
		e.authParam(), e.httpPkg)
	e.file.P("\tmux := %s.NewServeMux()", e.httpPkg)
	e.file.P("\tregister(mux, h%s)", e.authArg())
	e.file.P("\tall := append(")
	e.file.P("\t\t[]ServerOption{WithErrorOptions(DefaultErrorOptions())},")
	e.file.P("\t\topts...,")
	e.file.P("\t)")
	e.file.P("\treturn %s.Wrap(mux, %s.Resolve(all...))",
		e.httpxPkg, e.httpxPkg)
	e.file.P("}\n")

	e.file.Comment("Register adds every route to an existing mux, for a " +
		"service that owns its own router. The caller is then responsible " +
		"for installing apierr.Middleware, which DefaultErrorOptions " +
		"configures.")
	e.file.P("func Register(mux *%s.ServeMux, h Handler%s) {",
		e.httpPkg, e.authParam())
	e.file.P("\tregister(mux, h%s)", e.authArg())
	e.file.P("}\n")

	e.file.P("func register(mux *%s.ServeMux, h Handler%s) {",
		e.httpPkg, e.authParam())
	for _, o := range ops {
		e.file.P("\tmux.Handle(%s, handle%s(h%s))",
			strconv.Quote(o.op.Method+" "+o.op.Path), o.name, e.authArg())
	}
	e.file.P("}\n")
}

// errorOptions renders the errors block of the workspace configuration as a
// value the server seeds itself with.
//
// Without this the settings would be parsed, validated, and then ignored,
// leaving every binary to repeat a base URL the configuration already states.
func (e *serverEmitter) errorOptions() {
	cfg := e.ws.Config.Errors

	e.file.Comment("DefaultErrorOptions returns the errors block of the " +
		"workspace configuration. NewServer applies it before any option the " +
		"caller passes.")
	e.file.Comment("")
	e.file.Comment("A caller that needs to set something the configuration " +
		"cannot state, such as a TraceID extractor, should start from this " +
		"value: WithErrorOptions replaces the whole struct rather than " +
		"merging into it.")
	e.file.P("func DefaultErrorOptions() %s.Options {", e.apierrPkg)
	e.file.P("\treturn %s.Options{", e.apierrPkg)
	if cfg.TypeBaseURL != "" {
		e.file.P("\t\tTypeBaseURL: %s,", strconv.Quote(cfg.TypeBaseURL))
	}
	if cfg.ValidationTitle != "" {
		e.file.P(
			"\t\tValidationTitle: %s,",
			strconv.Quote(cfg.ValidationTitle),
		)
	}
	e.file.P("\t}")
	e.file.P("}\n")
}

// handler renders the authorize, decode, validate, dispatch, encode sequence
// for one operation.
func (e *serverEmitter) handler(o *operation) error {
	path := o.op.Params("path")
	query := o.op.Params("query")
	header := o.op.Params("header")
	body := o.op.RequestBody.JSON()

	e.file.P("func handle%s(h Handler%s) %s.Handler {",
		o.name, e.authParam(), e.httpPkg)
	e.file.P("\treturn %s.HandlerFunc("+
		"func(w %s.ResponseWriter, r *%s.Request) {",
		e.httpPkg, e.httpPkg, e.httpPkg)
	if o.op.Secured() {
		// Authentication runs before anything is parsed: there is no reason to
		// decode a body for a caller who will be turned away, and the context
		// the authenticator returns is the one the request carries from here
		// on, so req.Raw and the handler both see the caller's identity.
		e.file.P("\t\tctx, err := authorize%s(a, r)", o.name)
		e.file.P("\t\tif err != nil {")
		e.file.P("\t\t\t%s.Fail(r, err)", e.apierrPkg)
		e.file.P("\t\t\treturn")
		e.file.P("\t\t}")
		e.file.P("\t\tr = r.WithContext(ctx)")
		e.file.P("")
	}
	e.file.P("\t\treq := %s{Raw: r}", o.request)
	e.file.P("\t\tv := %s.New()", e.validatePkg)

	for _, p := range path {
		e.decodeParam(p, "Path", fmt.Sprintf("r.PathValue(%q)",
			p.Name), "\t\t")
	}
	if len(query) > 0 {
		e.file.P("\t\tq := r.URL.Query()")
		for _, p := range query {
			e.decodeParam(p, "Query", fmt.Sprintf("q.Get(%q)", p.Name), "\t\t")
		}
	}
	for _, p := range header {
		e.decodeParam(p, "Header",
			fmt.Sprintf("r.Header.Get(%q)", p.Name), "\t\t")
	}

	if body != nil && body.Schema != nil {
		e.decodeBody(o)
	}

	// Validation runs on everything that parsed, so one response reports every
	// problem rather than the first.
	if body != nil && body.Schema != nil &&
		needsCheck(e.gen.mapper, body.Schema) {
		var b strings.Builder
		e.validateValue(&b, body.Schema, "req.Body",
			fmt.Sprintf("v.Field(%s.Body)", e.validatePkg), "\t\t")
		e.file.WriteRaw(b.String())
	}
	e.file.P("\t\tif v.Failed() {")
	e.file.P("\t\t\t%s.Fail(r, %s.FromValidator(v))", e.apierrPkg, e.apierrPkg)
	e.file.P("\t\t\treturn")
	e.file.P("\t\t}")
	e.file.P("")

	if o.op.Secured() {
		// err already exists in this scope, from the authorize call above.
		e.file.P("\t\tvar result %s", e.resultType(o))
		e.file.P("\t\tresult, err = h.%s(r.Context(), req)", o.name)
	} else {
		e.file.P("\t\tresult, err := h.%s(r.Context(), req)", o.name)
	}
	e.file.P("\t\tif err != nil {")
	e.file.P("\t\t\t%s.Fail(r, err)", e.apierrPkg)
	e.file.P("\t\t\treturn")
	e.file.P("\t\t}")

	status := successStatus(o)
	if o.respType == "" {
		e.file.P("\t\t_ = result")
		e.file.P("\t\t%s.WriteStatus(w, %d)", e.httpxPkg, status)
	} else {
		e.file.P("\t\t%s.WriteJSON(w, r, %d, result)", e.httpxPkg, status)
	}
	e.file.P("\t})")
	e.file.P("}\n")
	return nil
}

// secured reports whether this specification declares security an operation
// actually uses.
func (e *serverEmitter) secured() bool { return len(e.schemes) > 0 }

// authParam is the Authenticator parameter the generated constructors take,
// empty for a specification that declares no security.
func (e *serverEmitter) authParam() string {
	if !e.secured() {
		return ""
	}
	return ", a Authenticator"
}

// authArg passes that parameter on.
func (e *serverEmitter) authArg() string {
	if !e.secured() {
		return ""
	}
	return ", a"
}

// successStatus is the status code written for a successful call.
func successStatus(o *operation) int {
	if o.success == nil {
		return http.StatusOK
	}
	n, err := strconv.Atoi(o.success.Status)
	if err != nil {
		return http.StatusOK
	}
	return n
}

// decodeParam renders the parse for one parameter.
func (e *serverEmitter) decodeParam(
	p *spec.Parameter, group, source, ind string,
) {
	field := fmt.Sprintf("req.%s.%s", group, naming.Exported(p.Name))
	part := map[string]string{
		"path": "Path", "query": "Query",
		"header": "Header", "cookie": "Cookie",
	}[p.In]
	loc := fmt.Sprintf("v.Field(%s.%s).Field(%q)", e.validatePkg, part, p.Name)

	f := e.file
	f.P("%sif raw := %s; raw != \"\" {", ind, source)
	e.parseInto(p.Schema, field, "raw", loc, ind+"\t", !p.Required)
	if p.Required {
		f.P("%s} else {", ind)
		f.P("%s\t%s.Fail(\"missing\", \"Field required\")", ind, loc)
	}
	f.P("%s}", ind)
}

// parseInto renders the conversion from a raw string into a typed field.
func (e *serverEmitter) parseInto(s *spec.Schema, field, raw, loc, ind string,
	optional bool,
) {
	f := e.file
	assign := func(expr string) {
		if optional {
			f.P("%s%s = %s.Some(%s)", ind, field,
				e.file.Import(OptionalImport), expr)
			return
		}
		f.P("%s%s = %s", ind, field, expr)
	}

	t, _ := e.gen.mapper.Scalar(s)
	switch {
	case t.Import != "":
		// A mapped type such as uuid.UUID or time.Time parses through
		// encoding.TextUnmarshaler; a type that does not implement it fails to
		// compile here, naming itself.
		f.P("%svar parsed %s", ind, e.file.ImportType(t.Expr, t.Import))
		f.P("%sif err := parsed.UnmarshalText([]byte(%s)); err != nil {",
			ind, raw)
		f.P("%s\t%s.Fail(%q, %q)", ind, loc,
			parseSlug(s), "Input should be a valid "+parseNoun(s))
		f.P("%s} else {", ind)
		assign("parsed")
		f.P("%s}", ind)

	case s.Is("integer"):
		bits := 64
		if t.Expr == "int32" {
			bits = 32
		}
		strconvPkg := e.file.Import("strconv")
		f.P("%sif n, err := %s.ParseInt(%s, 10, %d); err != nil {",
			ind, strconvPkg, raw, bits)
		f.P("%s\t%s.Fail(\"int_parsing\", "+
			"\"Input should be a valid integer\")", ind, loc)
		f.P("%s} else {", ind)
		assign(fmt.Sprintf("%s(n)", t.Expr))
		f.P("%s}", ind)

	case s.Is("number"):
		strconvPkg := e.file.Import("strconv")
		f.P("%sif n, err := %s.ParseFloat(%s, 64); err != nil {",
			ind, strconvPkg, raw)
		f.P("%s\t%s.Fail(\"float_parsing\", "+
			"\"Input should be a valid number\")", ind, loc)
		f.P("%s} else {", ind)
		assign(fmt.Sprintf("%s(n)", t.Expr))
		f.P("%s}", ind)

	case s.Is("boolean"):
		strconvPkg := e.file.Import("strconv")
		f.P("%sif b, err := %s.ParseBool(%s); err != nil {",
			ind, strconvPkg, raw)
		f.P("%s\t%s.Fail(\"bool_parsing\", "+
			"\"Input should be a valid boolean\")", ind, loc)
		f.P("%s} else {", ind)
		assign("b")
		f.P("%s}", ind)

	default:
		target := e.ws.Qualify(s, e.svc)
		if target == "" {
			target = t.Expr
		}
		assign(fmt.Sprintf("%s(%s)", target, raw))
	}

	// Constraints declared on the parameter apply once it has a value.
	if needsCheck(e.gen.mapper, s) {
		var b strings.Builder
		if optional {
			fmt.Fprintf(&b, "%sif val, ok := %s.Get(); ok {\n", ind, field)
			e.validateValue(&b, s, "val", loc, ind+"\t")
			fmt.Fprintf(&b, "%s}\n", ind)
		} else {
			e.validateValue(&b, s, field, loc, ind)
		}
		e.file.WriteRaw(b.String())
	}
}

// validateValue reuses the validation emitter so a parameter and a body field
// are checked by the same rules.
func (e *serverEmitter) validateValue(b *strings.Builder, s *spec.Schema,
	expr, loc, ind string,
) {
	ve := &validateEmitter{
		ws: e.ws, mapper: e.gen.mapper, file: e.file, svc: e.svc,
		validatePkg: e.validatePkg, patternName: map[string]string{},
	}
	ve.value(b, s, expr, loc, ind)
	for _, p := range ve.patterns {
		// A pattern discovered here is declared alongside the handler.
		e.file.P("var %s = %s.MustCompile(%s)",
			ve.patternName[p], e.file.Import("regexp"), backquote(p))
	}
}

// parseSlug names the failure for a mapped type, following Pydantic.
func parseSlug(s *spec.Schema) string {
	switch s.Format {
	case "uuid":
		return "uuid_parsing"
	case "date-time", "date":
		return "datetime_parsing"
	default:
		return "value_parsing"
	}
}

func parseNoun(s *spec.Schema) string {
	switch s.Format {
	case "uuid":
		return "UUID"
	case "date":
		return "date"
	case "date-time":
		return "datetime"
	default:
		return "value"
	}
}

func (e *serverEmitter) decodeBody(o *operation) {
	e.jsonPkg = e.file.Import("encoding/json")
	required := o.op.RequestBody != nil && o.op.RequestBody.Required

	e.file.P("\t\tif r.Body != nil {")
	e.file.P("\t\t\tdec := %s.NewDecoder(r.Body)", e.jsonPkg)
	e.file.P("\t\t\tif err := dec.Decode(&req.Body); err != nil {")
	e.file.P("\t\t\t\tv.Field(%s.Body).Fail(\"json_invalid\",", e.validatePkg)
	e.file.P("\t\t\t\t\t\"Input should be valid JSON\")")
	e.file.P("\t\t\t\t%s.Fail(r, %s.FromValidator(v))",
		e.apierrPkg, e.apierrPkg)
	e.file.P("\t\t\t\treturn")
	e.file.P("\t\t\t}")
	e.file.P("\t\t}")
	if required {
		e.file.P("\t\tif r.ContentLength == 0 {")
		e.file.P("\t\t\tv.Field(%s.Body).Fail(\"missing\", "+
			"\"Field required\")", e.validatePkg)
		e.file.P("\t\t}")
	}
}

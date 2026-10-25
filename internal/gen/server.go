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
	opTypes         // request and parameter struct names
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
			o := &operation{op: op, name: name, opTypes: e.types.opTypes[op]}

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
		e.methodDoc(o)
		e.file.P("\t%s(ctx %s.Context, req %s) (%s, error)",
			o.name, e.ctxPkg, o.request, e.resultType(o))
		e.file.P("")
	}
	e.file.P("}\n")
}

// methodDoc documents a Handler method with the operation's summary and
// description, then an example request and response: what the handler is
// given and what it should produce.
//
// The summary is kept as written rather than folded into a sentence that
// starts with the method name, since summaries are usually imperative
// ("Create an assistant") and lower-casing one does not make it read as
// "CreateAssistant creates an assistant".
func (e *serverEmitter) methodDoc(o *operation) {
	summary := strings.Join(strings.Fields(o.op.Summary), " ")
	desc := strings.TrimSpace(o.op.Description)
	request, response := e.examples(o)
	if summary == "" && desc == "" && request.Text == "" &&
		response.Text == "" {
		return
	}

	if summary != "" {
		e.file.CommentIn("\t", o.name+": "+summary)
	} else {
		e.file.CommentIn("\t", fmt.Sprintf("%s handles %s %s.",
			o.name, o.op.Method, o.op.Path))
	}
	if desc != "" && desc != summary {
		e.file.P("\t//")
		e.file.CommentIn("\t", desc)
	}
	e.exampleBlock("Example request", request)
	e.exampleBlock("Example response", response)
}

// exampleBlock writes a sample as a code block under a heading, or nothing
// when there is no sample.
func (e *serverEmitter) exampleBlock(heading string, s spec.Sample) {
	if s.Text == "" {
		return
	}
	if s.Title != "" {
		heading += " (" + s.Title + ")"
	}
	e.file.P("\t//")
	e.file.P("\t// %s:", heading)
	e.file.P("\t//")
	for _, line := range strings.Split(s.Text, "\n") {
		if strings.TrimSpace(line) == "" {
			e.file.P("\t//")
			continue
		}
		e.file.P("\t//\t%s", line)
	}
}

// examples picks one request and one response example for an operation. The
// standard places come first, the JSON request body and the success response,
// and the workspace's examples block fills in whichever side they leave
// empty. One of each is plenty to show the shape; the specification has the
// rest.
func (e *serverEmitter) examples(o *operation) (request, response spec.Sample) {
	if m := o.op.RequestBody.JSON(); m != nil && len(m.Examples) > 0 {
		request = m.Examples[0]
	}
	if m := o.success.JSON(); m != nil && len(m.Examples) > 0 {
		response = m.Examples[0]
	}
	cfg := e.svc.Cfg.Examples
	if cfg == nil {
		return request, response
	}
	found := o.op.Examples(&spec.ExampleKeys{
		Path:     cfg.PathKeys,
		Title:    cfg.TitleKeys,
		Request:  cfg.RequestKeys,
		Response: cfg.ResponseKeys,
	})
	for _, ex := range found {
		if request.Text == "" && ex.Request != "" {
			request = spec.Sample{Title: ex.Title, Text: ex.Request}
		}
		if response.Text == "" && ex.Response != "" {
			// The title is said once, on the request, when both come from
			// the same example.
			title := ex.Title
			if request.Title == ex.Title {
				title = ""
			}
			response = spec.Sample{Title: title, Text: ex.Response}
		}
	}
	return request, response
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
		query := o.op.Params(inQuery)
		header := o.op.Params("header")
		body := o.op.RequestBody.JSON()

		e.file.Comment(o.request + " is the decoded input to " + o.name + ".")
		e.file.P("type %s struct {", o.request)
		if len(path) > 0 {
			e.file.P("\tPath %s", o.path)
		}
		if len(query) > 0 {
			e.file.P("\tQuery %s", o.query)
		}
		if len(header) > 0 {
			e.file.P("\tHeader %s", o.header)
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

		e.paramStruct(o.path, path)
		e.paramStruct(o.query, query)
		e.paramStruct(o.header, header)
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
	query := o.op.Params(inQuery)
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
		src := fmt.Sprintf("r.PathValue(%q)", p.Name)
		if err := e.decodeParam(o, p, "Path", src); err != nil {
			return err
		}
	}
	if len(query) > 0 {
		e.file.P("\t\tq := r.URL.Query()")
		for _, p := range query {
			src := fmt.Sprintf("q.Get(%q)", p.Name)
			if err := e.decodeParam(o, p, "Query", src); err != nil {
				return err
			}
		}
	}
	for _, p := range header {
		src := fmt.Sprintf("r.Header.Get(%q)", p.Name)
		if err := e.decodeParam(o, p, "Header", src); err != nil {
			return err
		}
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

// The parameter location and styles the decoder distinguishes, spelled as
// the specification spells them.
const (
	inQuery     = "query"
	styleForm   = "form"
	styleSimple = "simple"
)

// paramDecode is what decoding one parameter needs to know about it.
type paramDecode struct {
	p      *spec.Parameter
	s      *spec.Schema // the parameter's schema, unwrapped
	owner  string       // the parameter struct, which names inline types
	goName string       // the parameter's field in that struct
	field  string       // the field, as an expression
	source string       // the parameter's single raw value, as an expression
	loc    string       // the parameter's validation location
	style  string
	expl   bool
}

const paramInd = "\t\t"

// decodeParam renders the parse for one parameter: a scalar from its raw
// value, and an array or an object from the strings its style lays out.
func (e *serverEmitter) decodeParam(
	o *operation, p *spec.Parameter, group, source string,
) error {
	part := map[string]string{
		"path": "Path", inQuery: "Query",
		"header": "Header", "cookie": "Cookie",
	}[p.In]
	d := &paramDecode{
		p: p,
		s: unwrap(p.Schema),
		owner: map[string]string{
			"path": o.path, inQuery: o.query, "header": o.header,
		}[p.In],
		goName: naming.Exported(p.Name),
		source: source,
		loc: fmt.Sprintf("v.Field(%s.%s).Field(%q)",
			e.validatePkg, part, p.Name),
	}
	d.field = fmt.Sprintf("req.%s.%s", group, d.goName)
	d.style, d.expl = paramStyle(p)

	switch classify(d.s) {
	case kindArray:
		return e.decodeArray(d)
	case kindMap, kindStruct:
		return e.decodeObject(o, d)
	}

	f := e.file
	f.P("%sif raw := %s; raw != \"\" {", paramInd, source)
	e.parseInto(p.Schema, d.field, "raw", d.loc, paramInd+"\t", !p.Required)
	e.missing(d)
	f.P("%s}", paramInd)
	return nil
}

// paramStyle returns how a parameter is serialized, with OpenAPI's defaults:
// form for a query parameter, simple elsewhere, and exploded only for form.
func paramStyle(p *spec.Parameter) (style string, explode bool) {
	style = p.Style
	if style == "" {
		style = styleSimple
		if p.In == inQuery || p.In == "cookie" {
			style = styleForm
		}
	}
	explode = style == styleForm
	if p.Explode != nil {
		explode = *p.Explode
	}
	return style, explode
}

// missing closes the presence check of a required parameter with its
// failure. An optional parameter that is absent is simply left unset.
func (e *serverEmitter) missing(d *paramDecode) {
	if d.p.Required {
		e.file.P("%s} else {", paramInd)
		e.file.P("%s\t%s.Fail(\"missing\", \"Field required\")",
			paramInd, d.loc)
	}
}

func unsupportedParam(d *paramDecode, what string) error {
	return fmt.Errorf("%s: parameter %q: %s is not supported yet",
		d.p.Pos, d.p.Name, what)
}

// isScalar reports whether a schema parses from one string.
func isScalar(s *spec.Schema) bool {
	switch classify(unwrap(s)) {
	case kindArray, kindMap, kindStruct:
		return false
	}
	return true
}

// decodeArray renders an array parameter: every value of a repeated query
// parameter when it is exploded, and the pieces of a delimited one when not.
func (e *serverEmitter) decodeArray(d *paramDecode) error {
	if !isScalar(d.s.Items) {
		return unsupportedParam(d, "an array of arrays or objects")
	}
	httpx := e.httpxPkg
	var raws string
	switch {
	case d.p.In == inQuery && d.expl &&
		(d.style == styleForm || d.style == "spaceDelimited" ||
			d.style == "pipeDelimited"):
		raws = fmt.Sprintf("q[%q]", d.p.Name)
	case d.p.In == inQuery && d.style == styleForm:
		raws = fmt.Sprintf("%s.Split(%s, \",\")", httpx, d.source)
	case d.p.In == inQuery && d.style == "spaceDelimited":
		raws = fmt.Sprintf("%s.Split(%s, \" \")", httpx, d.source)
	case d.p.In == inQuery && d.style == "pipeDelimited":
		raws = fmt.Sprintf("%s.Split(%s, \"|\")", httpx, d.source)
	case d.p.In != inQuery && d.style == styleSimple:
		raws = fmt.Sprintf("%s.Split(%s, \",\")", httpx, d.source)
	default:
		return unsupportedParam(d,
			fmt.Sprintf("an array in %s style", d.style))
	}
	elem, err := e.typeOf(d.s.Items, d.owner, d.goName+"Item")
	if err != nil {
		return err
	}

	f, ind := e.file, paramInd
	f.P("%sif raws := %s; len(raws) > 0 {", ind, raws)
	f.P("%s\tvals := make([]%s, len(raws))", ind, elem)
	f.P("%s\tfor i, raw := range raws {", ind)
	e.convert(d.s.Items, "vals[i]", "raw", d.loc+".Index(i)", ind+"\t\t",
		false)
	f.P("%s\t}", ind)
	e.assign(d.field, "vals", ind+"\t", !d.p.Required)
	e.missing(d)
	f.P("%s}", ind)
	e.check(d.s, d.field, d.loc, ind, !d.p.Required)
	return nil
}

// decodeObject renders an object parameter, from the key and value strings
// its style lays out: name[key]=value for deepObject, a query parameter per
// property for an exploded form, and delimited pairs otherwise.
func (e *serverEmitter) decodeObject(o *operation, d *paramDecode) error {
	isMap := classify(d.s) == kindMap
	if !isMap {
		if len(d.s.AllOf) > 0 {
			return unsupportedParam(d, "an object composed with allOf")
		}
		for _, p := range d.s.Properties {
			if !isScalar(p.Schema) {
				return unsupportedParam(d, "an object with a nested "+
					"array or object property")
			}
		}
	} else if v := d.s.AdditionalPro; v != nil && !isScalar(v) {
		return unsupportedParam(d, "a map of arrays or objects")
	}

	httpx := e.httpxPkg
	var src string
	fallible := false
	switch {
	case d.p.In == inQuery && d.style == "deepObject":
		src = fmt.Sprintf("%s.DeepObject(q, %q)", httpx, d.p.Name)
	case d.p.In == inQuery && d.style == styleForm && d.expl && !isMap:
		src = fmt.Sprintf("%s.Pick(q%s)", httpx,
			quotedArgs(propNames(d.s)))
	case d.p.In == inQuery && d.style == styleForm && d.expl:
		// A map's keys are whatever the operation's other query
		// parameters do not claim.
		var others []string
		for _, q := range o.op.Params(inQuery) {
			if q != d.p {
				others = append(others, q.Name)
			}
		}
		src = fmt.Sprintf("%s.Rest(q%s)", httpx, quotedArgs(others))
	case d.p.In == inQuery && d.style == styleForm,
		d.p.In != inQuery && d.style == styleSimple && !d.expl:
		src = fmt.Sprintf("%s.Pairs(%s, \",\")", httpx, d.source)
		fallible = true
	case d.p.In != inQuery && d.style == styleSimple:
		src = fmt.Sprintf("%s.KeyValues(%s)", httpx, d.source)
		fallible = true
	default:
		return unsupportedParam(d,
			fmt.Sprintf("an object in %s style", d.style))
	}
	typ, err := e.typeOf(d.s, d.owner, d.goName)
	if err != nil {
		return err
	}

	f, ind := e.file, paramInd
	if fallible {
		f.P("%sif m, err := %s; err != nil {", ind, src)
		f.P("%s\t%s.Fail(\"object_parsing\", "+
			"\"Input should be a valid object\")", ind, d.loc)
		f.P("%s} else if len(m) > 0 {", ind)
	} else {
		f.P("%sif m := %s; len(m) > 0 {", ind, src)
	}
	if isMap {
		f.P("%s\tval := make(%s, len(m))", ind, typ)
		f.P("%s\tfor key, raw := range m {", ind)
		if v := d.s.AdditionalPro; v == nil || v.Empty() {
			f.P("%s\t\tval[key] = raw", ind)
		} else {
			elem, err := e.typeOf(v, d.owner, d.goName+"Value")
			if err != nil {
				return err
			}
			f.P("%s\t\tvar item %s", ind, elem)
			e.convert(v, "item", "raw", d.loc+".Field(key)", ind+"\t\t",
				false)
			f.P("%s\t\tval[key] = item", ind)
		}
		f.P("%s\t}", ind)
	} else {
		f.P("%s\tvar val %s", ind, typ)
		for _, it := range structItems(e.ws, d.s) {
			p := it.prop
			loc := fmt.Sprintf("%s.Field(%q)", d.loc, p.Name)
			f.P("%s\tif raw, ok := m[%q]; ok {", ind, p.Name)
			e.convert(p.Schema, "val."+it.name, "raw", loc, ind+"\t\t",
				!p.Required || p.Schema.Nullable)
			if p.Required {
				f.P("%s\t} else {", ind)
				f.P("%s\t\t%s.Fail(\"missing\", \"Field required\")",
					ind, loc)
			}
			f.P("%s\t}", ind)
		}
	}
	e.assign(d.field, "val", ind+"\t", !d.p.Required)
	e.missing(d)
	f.P("%s}", ind)
	e.check(d.s, d.field, d.loc, ind, !d.p.Required)
	return nil
}

func propNames(s *spec.Schema) []string {
	out := make([]string, 0, len(s.Properties))
	for _, p := range s.Properties {
		out = append(out, p.Name)
	}
	return out
}

// quotedArgs renders names as trailing arguments: ", "a", "b"".
func quotedArgs(names []string) string {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, ", %q", n)
	}
	return b.String()
}

// assign stores a parsed value into a field, wrapped when it is optional.
func (e *serverEmitter) assign(field, expr, ind string, optional bool) {
	if optional {
		e.file.P("%s%s = %s.Some(%s)", ind, field,
			e.file.Import(OptionalImport), expr)
		return
	}
	e.file.P("%s%s = %s", ind, field, expr)
}

// parseInto renders the conversion from a raw string into a typed field,
// and then the checks the schema declares on it.
func (e *serverEmitter) parseInto(s *spec.Schema, field, raw, loc, ind string,
	optional bool,
) {
	e.convert(s, field, raw, loc, ind, optional)
	e.check(s, field, loc, ind, optional)
}

// convert renders the conversion from a raw string into a typed field.
func (e *serverEmitter) convert(s *spec.Schema, field, raw, loc, ind string,
	optional bool,
) {
	s = unwrap(s)
	f := e.file
	assign := func(expr string) { e.assign(field, expr, ind, optional) }

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
		// The field was declared with the schema's own type, which for an
		// inline or whole-file enum is a name synthesized for it rather than
		// one the workspace knows: converting to the base type instead would
		// not assign.
		target := e.types.nameOf(s)
		if target == "" {
			target = t.Expr
		}
		assign(fmt.Sprintf("%s(%s)", target, raw))
	}
}

// check renders the constraints declared on a parameter, which apply once it
// has a value.
func (e *serverEmitter) check(s *spec.Schema, field, loc, ind string,
	optional bool,
) {
	s = unwrap(s)
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

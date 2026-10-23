package spec

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Document is a parsed OpenAPI document.
type Document struct {
	File    string
	OpenAPI string
	Info    Info
	Servers []Server

	// Paths in document order.
	Paths []*PathItem
	// Components holds every schema declared under components/schemas in this
	// file, in document order. A shared root generates from this list; a
	// service spec generates only what its operations reach.
	Components []*Schema

	// SecuritySchemes holds components/securitySchemes in document order, and
	// Security the document-wide requirements an operation inherits when it
	// declares none of its own.
	SecuritySchemes []*SecurityScheme
	Security        []Requirement
}

// Info is the document's info block.
type Info struct {
	Title       string
	Version     string
	Description string
}

// Server is one entry of the servers list.
type Server struct {
	URL         string
	Description string
}

// PathItem is one templated path and the operations on it.
type PathItem struct {
	Path       string
	Pos        pos
	Parameters []*Parameter
	Operations []*Operation
}

// Operation is one method on one path.
type Operation struct {
	Method      string // upper case
	Path        string
	OperationID string
	Summary     string
	Description string
	Tags        []string
	Deprecated  bool
	Pos         pos

	// Parameters is the path item's parameters merged with the operation's own,
	// with the operation winning on a name/in collision.
	Parameters  []*Parameter
	RequestBody *RequestBody
	Responses   []*Response

	// Security is the effective requirement list, which is the operation's own
	// when HasSecurity is set and the document's otherwise. An operation that
	// writes an empty list has HasSecurity set and no requirements, which is
	// how it opts out of a document-wide default.
	Security    []Requirement
	HasSecurity bool

	// node is the operation object as written, for reading what the model
	// does not, such as examples kept under an extension.
	node *yaml.Node
}

// Params returns the parameters in a given location, preserving order.
func (o *Operation) Params(in string) []*Parameter {
	var out []*Parameter
	for _, p := range o.Parameters {
		if p.In == in {
			out = append(out, p)
		}
	}
	return out
}

// Success returns the responses with a 2xx status, in ascending status order.
func (o *Operation) Success() []*Response {
	var out []*Response
	for _, r := range o.Responses {
		if strings.HasPrefix(r.Status, "2") {
			out = append(out, r)
		}
	}
	return out
}

// Parameter is a query, path, header, or cookie parameter.
type Parameter struct {
	Name        string
	In          string // query, path, header, cookie
	Description string
	Required    bool
	Deprecated  bool
	Schema      *Schema
	Style       string
	Explode     *bool
	Loc         Location // set when declared under components/parameters
	Pos         pos
}

// RequestBody is an operation's request body.
type RequestBody struct {
	Description string
	Required    bool
	Content     []*MediaType
	Loc         Location
	Pos         pos
}

// MediaType is one entry of a content map.
type MediaType struct {
	MediaType string
	Schema    *Schema
	Examples  []Sample // example, or each of examples in order
	Pos       pos
}

// JSON returns the application/json media type, or nil. A "+json" suffix such
// as application/problem+json counts.
func jsonOf(content []*MediaType) *MediaType {
	for _, m := range content {
		base, _, _ := strings.Cut(m.MediaType, ";")
		base = strings.TrimSpace(base)
		if base == "application/json" || strings.HasSuffix(base, "+json") {
			return m
		}
	}
	return nil
}

// JSON returns the JSON media type of the body, or nil.
func (b *RequestBody) JSON() *MediaType {
	if b == nil {
		return nil
	}
	return jsonOf(b.Content)
}

// Response is one entry of an operation's responses.
type Response struct {
	Status      string // "200", "2XX", or "default"
	Description string
	Headers     []*Header
	Content     []*MediaType
	Loc         Location
	Pos         pos
}

// JSON returns the JSON media type of the response, or nil.
func (r *Response) JSON() *MediaType {
	if r == nil {
		return nil
	}
	return jsonOf(r.Content)
}

// Header is one response header.
type Header struct {
	Name        string
	Description string
	Required    bool
	Deprecated  bool
	Schema      *Schema
	Pos         pos
}

var methods = []string{
	"get", "put", "post", "delete", "options", "head", "patch", "trace",
}

func isMethod(s string) bool {
	for _, m := range methods {
		if m == s {
			return true
		}
	}
	return false
}

// deref follows a $ref on a non-schema object, returning the file the target
// lives in so that the target's own relative refs resolve correctly.
func (l *Loader) deref(
	file string,
	n *yaml.Node,
) (string, *yaml.Node, Location, error) {
	loc, ok, err := refOf(file, n)
	if err != nil {
		return "", nil, Location{}, err
	}
	if !ok {
		return file, n, Location{}, nil
	}
	target, err := l.nodeAt(loc)
	if err != nil {
		return "", nil, Location{}, err
	}
	// A ref may point at another ref.
	_, chained, err := refOf(loc.File, target)
	if err != nil {
		return "", nil, Location{}, err
	}
	if chained {
		f, t, inner, err := l.deref(loc.File, target)
		if inner.IsZero() {
			inner = loc
		}
		return f, t, inner, err
	}
	return loc.File, target, loc, nil
}

// Document parses the OpenAPI document at an absolute path.
func (l *Loader) Document(abs string) (*Document, error) {
	root, err := l.File(abs)
	if err != nil {
		return nil, err
	}
	d := &Document{File: abs}

	f := &fields{file: abs}
	d.OpenAPI = f.str(mapGet(root, "openapi"))
	if info := mapGet(root, "info"); info != nil {
		d.Info.Title = f.str(mapGet(info, "title"))
		d.Info.Version = f.str(mapGet(info, "version"))
		d.Info.Description = f.str(mapGet(info, "description"))
	}
	if f.err != nil {
		return nil, f.err
	}
	if err := seqEach(mapGet(root, "servers"), func(_ int, n *yaml.Node) error {
		url, err := strVal(abs, mapGet(n, "url"))
		if err != nil {
			return err
		}
		desc, err := strVal(abs, mapGet(n, "description"))
		if err != nil {
			return err
		}
		d.Servers = append(d.Servers, Server{URL: url, Description: desc})
		return nil
	}); err != nil {
		return nil, err
	}

	// components/schemas, in document order.
	if schemas := mapGet(
		mapGet(root, "components"),
		"schemas",
	); schemas != nil {
		err := mapEach(schemas, func(name string, _, _ *yaml.Node) error {
			ptr := "/components/schemas/" + escapePointerToken(name)
			s, err := l.Schema(Location{abs, ptr})
			if err != nil {
				return err
			}
			d.Components = append(d.Components, s)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if schemes := mapGet(
		mapGet(root, "components"),
		"securitySchemes",
	); schemes != nil {
		d.SecuritySchemes, err = l.securitySchemes(abs, schemes)
		if err != nil {
			return nil, err
		}
	}
	if sec := mapGet(root, "security"); sec != nil {
		if d.Security, err = securityList(abs, sec); err != nil {
			return nil, err
		}
	}

	if paths := mapGet(root, "paths"); paths != nil {
		err := mapEach(paths, func(
			path string,
			keyNode,
			item *yaml.Node,
		) error {
			pi, err := l.pathItem(abs, path, keyNode, item)
			if err != nil {
				return fmt.Errorf("paths %s: %w", path, err)
			}
			d.Paths = append(d.Paths, pi)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if err := d.assignOperationIDs(); err != nil {
		return nil, err
	}
	if err := d.resolveSecurity(); err != nil {
		return nil, err
	}
	return d, nil
}

func (l *Loader) pathItem(
	file,
	path string,
	keyNode,
	n *yaml.Node,
) (*PathItem, error) {
	file, n, _, err := l.deref(file, n)
	if err != nil {
		return nil, err
	}
	pi := &PathItem{Path: path, Pos: at(file, keyNode)}

	if params := mapGet(n, "parameters"); params != nil {
		if pi.Parameters, err = l.parameters(file, params); err != nil {
			return nil, err
		}
	}

	err = mapEach(n, func(key string, kn, v *yaml.Node) error {
		if !isMethod(strings.ToLower(key)) {
			return nil
		}
		op, err := l.operation(file, strings.ToUpper(key), path, kn, v)
		if err != nil {
			return fmt.Errorf("%s: %w", strings.ToUpper(key), err)
		}
		op.Parameters = mergeParams(pi.Parameters, op.Parameters)
		if err := checkPathParams(path, op); err != nil {
			return err
		}
		pi.Operations = append(pi.Operations, op)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pi, nil
}

func (l *Loader) operation(
	file,
	method,
	path string,
	keyNode,
	n *yaml.Node,
) (*Operation, error) {
	op := &Operation{
		Method: method, Path: path, Pos: at(file, keyNode), node: n,
	}
	var err error

	f := &fields{file: file}
	op.OperationID = f.str(mapGet(n, "operationId"))
	op.Summary = f.str(mapGet(n, "summary"))
	op.Description = f.str(mapGet(n, "description"))
	op.Deprecated = f.bool(mapGet(n, "deprecated"))
	if f.err != nil {
		return nil, f.err
	}
	if op.Tags, err = strsVal(file, mapGet(n, "tags")); err != nil {
		return nil, err
	}
	if params := mapGet(n, "parameters"); params != nil {
		if op.Parameters, err = l.parameters(file, params); err != nil {
			return nil, err
		}
	}
	if body := mapGet(n, "requestBody"); body != nil {
		if op.RequestBody, err = l.requestBody(file, body); err != nil {
			return nil, fmt.Errorf("requestBody: %w", err)
		}
	}
	if sec := mapGet(n, "security"); sec != nil {
		// Present but empty is meaningful: it overrides the document default
		// rather than inheriting it, so record that the key was written.
		op.HasSecurity = true
		if op.Security, err = securityList(file, sec); err != nil {
			return nil, err
		}
	}
	if resp := mapGet(n, "responses"); resp != nil {
		if op.Responses, err = l.responses(file, resp); err != nil {
			return nil, fmt.Errorf("responses: %w", err)
		}
	}
	if len(op.Responses) == 0 {
		return nil, errf(at(file, n), "operation declares no responses")
	}
	return op, nil
}

func (l *Loader) parameters(file string, n *yaml.Node) ([]*Parameter, error) {
	var out []*Parameter
	err := seqEach(n, func(i int, e *yaml.Node) error {
		pfile, pn, loc, err := l.deref(file, e)
		if err != nil {
			return err
		}
		p := &Parameter{Loc: loc, Pos: at(pfile, pn)}
		if p.Name, err = strVal(pfile, mapGet(pn, "name")); err != nil {
			return err
		}
		if p.In, err = strVal(pfile, mapGet(pn, "in")); err != nil {
			return err
		}
		if p.Name == "" || p.In == "" {
			return errf(
				at(pfile, pn),
				"parameter %d: name and in are required",
				i,
			)
		}
		switch p.In {
		case "query", "path", "header", "cookie":
		default:
			return errf(at(pfile, pn), "parameter %q: in must be query, path, "+
				"header, or cookie, not %q", p.Name, p.In)
		}
		f := &fields{file: pfile}
		p.Description = f.str(mapGet(pn, "description"))
		p.Required = f.bool(mapGet(pn, "required"))
		p.Deprecated = f.bool(mapGet(pn, "deprecated"))
		p.Style = f.str(mapGet(pn, "style"))
		if f.err != nil {
			return f.err
		}
		if ex := mapGet(pn, "explode"); ex != nil {
			b, err := boolVal(pfile, ex)
			if err != nil {
				return err
			}
			p.Explode = &b
		}
		// A path parameter is required by definition; accepting it as optional
		// would generate a route that can never match.
		if p.In == "path" && !p.Required {
			return errf(
				at(pfile, pn),
				"path parameter %q must be required",
				p.Name,
			)
		}
		if p.Schema, err = l.SchemaNode(
			pfile,
			mapGet(pn, "schema"),
		); err != nil {
			return fmt.Errorf("parameter %q: %w", p.Name, err)
		}
		if p.Schema == nil {
			return errf(
				at(pfile, pn),
				"parameter %q: schema is required",
				p.Name,
			)
		}
		out = append(out, p)
		return nil
	})
	return out, err
}

func (l *Loader) requestBody(file string, n *yaml.Node) (*RequestBody, error) {
	bfile, bn, loc, err := l.deref(file, n)
	if err != nil {
		return nil, err
	}
	b := &RequestBody{Loc: loc, Pos: at(bfile, bn)}
	f := &fields{file: bfile}
	b.Description = f.str(mapGet(bn, "description"))
	b.Required = f.bool(mapGet(bn, "required"))
	if f.err != nil {
		return nil, f.err
	}
	if b.Content, err = l.content(bfile, mapGet(bn, "content")); err != nil {
		return nil, err
	}
	if len(b.Content) == 0 {
		return nil, errf(at(bfile, bn), "requestBody declares no content")
	}
	return b, nil
}

func (l *Loader) content(file string, n *yaml.Node) ([]*MediaType, error) {
	if n == nil {
		return nil, nil
	}
	var out []*MediaType
	err := mapEach(n, func(name string, kn, v *yaml.Node) error {
		s, err := l.SchemaNode(file, mapGet(v, "schema"))
		if err != nil {
			return fmt.Errorf("content %q: %w", name, err)
		}
		examples, err := l.mediaExamples(file, v)
		if err != nil {
			return fmt.Errorf("content %q: examples: %w", name, err)
		}
		out = append(out, &MediaType{
			MediaType: name, Schema: s, Examples: examples, Pos: at(file, kn),
		})
		return nil
	})
	return out, err
}

func (l *Loader) responses(file string, n *yaml.Node) ([]*Response, error) {
	var out []*Response
	err := mapEach(n, func(status string, kn, v *yaml.Node) error {
		rfile, rn, loc, err := l.deref(file, v)
		if err != nil {
			return err
		}
		if err := validStatus(status); err != nil {
			return errf(at(file, kn), "%v", err)
		}
		r := &Response{Status: status, Loc: loc, Pos: at(rfile, rn)}
		r.Description, err = strVal(rfile, mapGet(rn, "description"))
		if err != nil {
			return err
		}
		if r.Content, err = l.content(
			rfile,
			mapGet(rn, "content"),
		); err != nil {
			return fmt.Errorf("response %s: %w", status, err)
		}
		if headers := mapGet(rn, "headers"); headers != nil {
			err := mapEach(headers, func(
				name string,
				hkn,
				hv *yaml.Node,
			) error {
				hfile, hn, _, err := l.deref(rfile, hv)
				if err != nil {
					return err
				}
				h := &Header{Name: name, Pos: at(hfile, hn)}
				f := &fields{file: hfile}
				h.Description = f.str(mapGet(hn, "description"))
				h.Required = f.bool(mapGet(hn, "required"))
				h.Deprecated = f.bool(mapGet(hn, "deprecated"))
				if f.err != nil {
					return f.err
				}
				if h.Schema, err = l.SchemaNode(
					hfile,
					mapGet(hn, "schema"),
				); err != nil {
					return fmt.Errorf("header %q: %w", name, err)
				}
				r.Headers = append(r.Headers, h)
				return nil
			})
			if err != nil {
				return err
			}
		}
		out = append(out, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Order by status so generated switch statements are stable; "default"
	// last.
	sort.SliceStable(out, func(i, j int) bool {
		return statusRank(out[i].Status) < statusRank(out[j].Status)
	})
	return out, nil
}

func validStatus(s string) error {
	if s == "default" {
		return nil
	}
	if len(s) == 3 && s[0] >= '1' && s[0] <= '5' {
		if s[1] == 'X' && s[2] == 'X' {
			return nil
		}
		if s[1] >= '0' && s[1] <= '9' && s[2] >= '0' && s[2] <= '9' {
			return nil
		}
	}
	return fmt.Errorf(
		"response key %q is not a status code, a NXX range, or \"default\"",
		s,
	)
}

func statusRank(s string) int {
	if s == "default" {
		return 1000
	}
	rank := 0
	for _, c := range s {
		rank *= 10
		if c >= '0' && c <= '9' {
			rank += int(c - '0')
		} else {
			rank += 9 // NXX sorts after the explicit codes in its range
		}
	}
	return rank
}

// mergeParams overlays operation parameters onto path item parameters. A
// name/in match means the operation's declaration wins.
func mergeParams(pathLevel, opLevel []*Parameter) []*Parameter {
	key := func(p *Parameter) string { return p.In + "\x00" + p.Name }
	overridden := map[string]bool{}
	for _, p := range opLevel {
		overridden[key(p)] = true
	}
	out := make([]*Parameter, 0, len(pathLevel)+len(opLevel))
	for _, p := range pathLevel {
		if !overridden[key(p)] {
			out = append(out, p)
		}
	}
	return append(out, opLevel...)
}

// checkPathParams verifies the templated path and the declared path parameters
// agree, which is the difference between a route that works and one that
// silently 404s.
func checkPathParams(path string, op *Operation) error {
	inPath := map[string]bool{}
	for _, name := range PathTemplateVars(path) {
		inPath[name] = true
	}
	declared := map[string]bool{}
	for _, p := range op.Params("path") {
		declared[p.Name] = true
		if !inPath[p.Name] {
			return errf(op.Pos, "path parameter %q is declared but does not "+
				"appear in the path template %q", p.Name, path)
		}
	}
	var missing []string
	for name := range inPath {
		if !declared[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return errf(op.Pos, "path template %q uses %s, which %s not declared "+
			"as path parameter(s)",
			path, strings.Join(
				quoteAll(missing),
				", ",
			), plural(len(missing), "is", "are"))
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// PathTemplateVars returns the {name} variables of a templated path, in order.
func PathTemplateVars(path string) []string {
	var out []string
	for {
		open := strings.IndexByte(path, '{')
		if open < 0 {
			return out
		}
		end := strings.IndexByte(path[open:], '}')
		if end < 0 {
			return out
		}
		out = append(out, path[open+1:open+end])
		path = path[open+end+1:]
	}
}

// assignOperationIDs fills in any missing operationId and rejects duplicates,
// since the id becomes a Go method name.
func (d *Document) assignOperationIDs() error {
	seen := map[string]*Operation{}
	for _, pi := range d.Paths {
		for _, op := range pi.Operations {
			if op.OperationID == "" {
				op.OperationID = deriveOperationID(op.Method, op.Path)
			}
			if prev, dup := seen[op.OperationID]; dup {
				return errf(op.Pos, "operationId %q is already used by %s %s "+
					"at %s",
					op.OperationID, prev.Method, prev.Path, prev.Pos)
			}
			seen[op.OperationID] = op
		}
	}
	return nil
}

// deriveOperationID builds a stable id from a method and path, for specs that
// omit operationId: GET /users/{id}/roles becomes getUsersIdRoles.
func deriveOperationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, seg := range strings.Split(path, "/") {
		seg = strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		if seg == "" {
			continue
		}
		clean := make([]rune, 0, len(seg))
		upper := true
		for _, r := range seg {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
				r >= '0' && r <= '9':
				if upper {
					clean = append(clean, []rune(strings.ToUpper(string(r)))...)
					upper = false
				} else {
					clean = append(clean, r)
				}
			default:
				upper = true
			}
		}
		b.WriteString(string(clean))
	}
	return b.String()
}

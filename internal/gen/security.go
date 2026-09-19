package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/franco-grobler/go-api-gen/internal/naming"
	"github.com/franco-grobler/go-api-gen/internal/spec"
)

// usedSchemes returns the security schemes at least one operation requires, in
// the order the document declares them.
//
// A scheme declared and never referenced produces no method: an interface the
// server never calls is a compile error waiting for somebody who reads the
// specification too literally.
func (e *serverEmitter) usedSchemes() []*spec.SecurityScheme {
	doc := e.svc.Doc
	used := map[string]bool{}
	for _, pi := range doc.Paths {
		for _, op := range pi.Operations {
			for _, req := range op.Security {
				for _, ref := range req.Schemes {
					used[ref.Name] = true
				}
			}
		}
	}

	var out []*spec.SecurityScheme
	for _, s := range doc.SecuritySchemes {
		if used[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

// authMethod is the Authenticator method a scheme is verified through.
func authMethod(s *spec.SecurityScheme) string {
	return naming.Exported(s.Name)
}

// authHelper is the generated function that locates a scheme's credential.
func authHelper(s *spec.SecurityScheme) string {
	return "auth" + naming.Exported(s.Name)
}

// authParams is the credential a scheme's method receives, after ctx and the
// request. It is what the specification says arrives, not a bag of strings.
func authParams(s *spec.SecurityScheme) string {
	switch {
	case s.Type == spec.SchemeAPIKey:
		return "key string"
	case s.Basic():
		return "username, password string"
	case s.Scoped():
		return "token string, scopes []string"
	case s.Bearer():
		return "token string"
	case s.Type == spec.SchemeMutualTLS:
		return ""
	default:
		return "credential string"
	}
}

// security renders the Authenticator interface and everything that calls it.
func (e *serverEmitter) security(ops []*operation) {
	schemes := e.schemes
	if len(schemes) == 0 {
		return
	}

	e.file.Comment("Authenticator verifies the credentials this " +
		"specification declares.")
	e.file.Comment("")
	e.file.Comment("The server finds each credential where the specification " +
		"says it lives and calls the matching method before any handler " +
		"runs. Returning a context is how the caller's identity reaches the " +
		"handler: whatever a method returns becomes the ctx the handler is " +
		"given.")
	e.file.Comment("")
	e.file.Comment("Returning an error refuses the request. An " +
		"*apierr.Problem is rendered as it stands, so a method that wants a " +
		"403 rather than a 401 says so; anything else becomes a 401.")
	e.file.P("type Authenticator interface {")
	for i, s := range schemes {
		if i > 0 {
			e.file.P("")
		}
		for _, line := range authDoc(s) {
			e.file.P("\t// %s", line)
		}
		params := authParams(s)
		if params != "" {
			params = ", " + params
		}
		e.file.P("\t%s(ctx %s.Context, r *%s.Request%s) "+
			"(%s.Context, error)",
			authMethod(s), e.ctxPkg, e.httpPkg, params, e.ctxPkg)
	}
	e.file.P("}\n")

	e.authPlumbing()
	for _, s := range schemes {
		e.authExtractor(s)
	}
	e.authRequirements(ops)
}

// authDoc describes a scheme's method in terms of where its credential comes
// from, since that is the part the implementer cannot see from the signature.
func authDoc(s *spec.SecurityScheme) []string {
	var what string
	switch {
	case s.Type == spec.SchemeAPIKey:
		what = fmt.Sprintf("the %s %s", s.ParamName, s.In)
	case s.Basic():
		what = "the Authorization header's basic credentials"
	case s.Scoped():
		what = "the bearer token, against the scopes the operation asks for"
	case s.Bearer():
		what = "the bearer token from the Authorization header"
	case s.Type == spec.SchemeMutualTLS:
		what = "the request's TLS peer certificates"
	default:
		what = "the Authorization header's " + s.Scheme + " credentials"
	}
	lines := []string{authMethod(s) + " verifies " + what + "."}
	if s.Description != "" {
		lines = append(lines, "", firstLine(s.Description))
	}
	return lines
}

// authPlumbing renders the two pieces every secured server needs: a step, and
// the conjunction of steps that makes up one requirement.
func (e *serverEmitter) authPlumbing() {
	e.file.Comment("authStep is one scheme within a requirement, together " +
		"with the scopes the operation asks of it.")
	e.file.P("type authStep struct {")
	e.file.P("\tverify func(Authenticator, %s.Context, *%s.Request, "+
		"[]string) (%s.Context, error)", e.ctxPkg, e.httpPkg, e.ctxPkg)
	e.file.P("\tscopes []string")
	e.file.P("}\n")

	e.file.Comment("authAll satisfies one requirement: every step in it, in " +
		"order, each handed the context the step before returned. A " +
		"requirement is an AND, so the first refusal ends it.")
	e.file.P("func authAll(a Authenticator, r *%s.Request, "+
		"steps ...authStep) (%s.Context, error) {", e.httpPkg, e.ctxPkg)
	e.file.P("\tctx := r.Context()")
	e.file.P("\tfor _, step := range steps {")
	e.file.P("\t\tnext, err := step.verify(a, ctx, r, step.scopes)")
	e.file.P("\t\tif err != nil {")
	e.file.P("\t\t\treturn nil, err")
	e.file.P("\t\t}")
	e.file.P("\t\tctx = next")
	e.file.P("\t}")
	e.file.P("\treturn ctx, nil")
	e.file.P("}\n")

	e.file.Comment("authMissing reports a credential that never arrived. " +
		"RFC 9110 requires a 401 to carry a challenge naming what the client " +
		"should send instead, so a scheme that defines one sends it.")
	e.file.P("func authMissing(scheme, challenge string) error {")
	e.file.P("\tp := %s.Unauthorized(\"no credentials for %%s\", scheme)."+
		"\n\t\tWithExtra(\"scheme\", scheme)", e.apierrPkg)
	e.file.P("\tif challenge != \"\" {")
	e.file.P("\t\tp = p.WithHeader(\"WWW-Authenticate\", challenge)")
	e.file.P("\t}")
	e.file.P("\treturn p")
	e.file.P("}\n")
}

// authExtractor renders the function that locates one scheme's credential and
// hands it to the Authenticator.
func (e *serverEmitter) authExtractor(s *spec.SecurityScheme) {
	name := strconv.Quote(s.Name)
	challenge := strconv.Quote(s.Challenge())

	e.file.Comment(authHelper(s) + " reads the credential " + s.Name +
		" declares and verifies it.")
	e.file.P("func %s(a Authenticator, ctx %s.Context, r *%s.Request, "+
		"scopes []string) (%s.Context, error) {",
		authHelper(s), e.ctxPkg, e.httpPkg, e.ctxPkg)

	call := func(args string) {
		e.file.P("\treturn a.%s(ctx, r%s)", authMethod(s), args)
	}

	switch {
	case s.Type == spec.SchemeAPIKey:
		e.file.P("\tkey, ok := %s.APIKey(r, %s, %s)",
			e.httpxPkg, strconv.Quote(s.In), strconv.Quote(s.ParamName))
		e.file.P("\tif !ok {")
		e.file.P("\t\treturn nil, authMissing(%s, %s)", name, challenge)
		e.file.P("\t}")
		call(", key")
	case s.Basic():
		e.file.P("\tusername, password, ok := r.BasicAuth()")
		e.file.P("\tif !ok {")
		e.file.P("\t\treturn nil, authMissing(%s, %s)", name, challenge)
		e.file.P("\t}")
		call(", username, password")
	case s.Bearer():
		e.file.P("\ttoken, ok := %s.BearerToken(r)", e.httpxPkg)
		e.file.P("\tif !ok {")
		e.file.P("\t\treturn nil, authMissing(%s, %s)", name, challenge)
		e.file.P("\t}")
		if s.Scoped() {
			call(", token, scopes")
		} else {
			call(", token")
		}
	case s.Type == spec.SchemeMutualTLS:
		e.file.P("\t// A client certificate is the transport's business; the " +
			"handler is\n\t// handed the request so it can read r.TLS.")
		e.file.P("\tif r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {")
		e.file.P("\t\treturn nil, authMissing(%s, \"\")", name)
		e.file.P("\t}")
		call("")
	default:
		e.file.P("\tcredential, ok := %s.SchemeCredential(r, %s)",
			e.httpxPkg, strconv.Quote(s.Scheme))
		e.file.P("\tif !ok {")
		e.file.P("\t\treturn nil, authMissing(%s, %s)", name, challenge)
		e.file.P("\t}")
		call(", credential")
	}

	// Every extractor takes a scopes argument so that one func type covers
	// them all, and ignores it unless the scheme is scoped.
	e.file.P("}\n")
}

// authRequirements renders one authorize function per secured operation,
// under the name operations() already settled on so that authorizeX sits
// beside handleX.
func (e *serverEmitter) authRequirements(ops []*operation) {
	for _, o := range ops {
		if o.op.Secured() {
			e.authorize(o.op, o.name)
		}
	}
}

// authorize renders the alternatives one operation accepts. The requirements
// are an OR, so the first that succeeds wins and its context is the one the
// handler receives.
func (e *serverEmitter) authorize(op *spec.Operation, name string) {
	e.file.Comment("authorize" + name + " applies the security " +
		op.OperationID + " declares: " + describeSecurity(op.Security) + ".")
	e.file.P("func authorize%s(a Authenticator, r *%s.Request) "+
		"(%s.Context, error) {", name, e.httpPkg, e.ctxPkg)

	e.file.P("\tvar failed error")
	for _, req := range op.Security {
		if req.Optional() {
			// Handled after the alternatives, so a caller who did present a
			// credential is identified by it rather than waved through.
			continue
		}
		e.file.P("")
		e.file.P("\t// %s", describeRequirement(req))
		steps := make([]string, 0, len(req.Schemes))
		for _, ref := range req.Schemes {
			scheme := e.svc.Doc.SecurityScheme(ref.Name)
			steps = append(steps, fmt.Sprintf("authStep{%s, %s}",
				authHelper(scheme), scopeLiteral(ref.Scopes)))
		}
		if len(steps) == 1 {
			e.file.P("\tif ctx, err := authAll(a, r, %s); err == nil {",
				steps[0])
		} else {
			e.file.P("\tif ctx, err := authAll(a, r,")
			for _, step := range steps {
				e.file.P("\t\t%s,", step)
			}
			e.file.P("\t); err == nil {")
		}
		e.file.P("\t\treturn ctx, nil")
		e.file.P("\t} else if failed == nil {")
		e.file.P("\t\tfailed = err")
		e.file.P("\t}")
	}
	e.file.P("")
	if op.Anonymous() {
		e.file.Comment("\tThe specification lists an empty requirement, so a " +
			"caller who presented nothing usable continues unauthenticated.")
		e.file.P("\t_ = failed")
		e.file.P("\treturn r.Context(), nil")
	} else {
		e.file.P("\treturn nil, failed")
	}
	e.file.P("}\n")
}

func scopeLiteral(scopes []string) string {
	if len(scopes) == 0 {
		return "nil"
	}
	quoted := make([]string, len(scopes))
	for i, s := range scopes {
		quoted[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// describeSecurity renders a requirement list the way a reader would say it:
// alternatives separated by "or", each an AND of the schemes in it.
func describeSecurity(reqs []spec.Requirement) string {
	parts := make([]string, 0, len(reqs))
	for _, req := range reqs {
		if req.Optional() {
			parts = append(parts, "nothing")
			continue
		}
		parts = append(parts, describeRequirement(req))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", or ")
}

// describeRequirement renders one requirement as its schemes, AND-ed.
func describeRequirement(req spec.Requirement) string {
	names := make([]string, len(req.Schemes))
	for i, ref := range req.Schemes {
		names[i] = ref.Name
		if len(ref.Scopes) > 0 {
			names[i] += " (" + strings.Join(ref.Scopes, ", ") + ")"
		}
	}
	return strings.Join(names, " and ")
}

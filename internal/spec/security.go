package spec

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Security scheme types, as OpenAPI spells them.
const (
	SchemeAPIKey        = "apiKey"
	SchemeHTTP          = "http"
	SchemeOAuth2        = "oauth2"
	SchemeOpenIDConnect = "openIdConnect"
	SchemeMutualTLS     = "mutualTLS"
)

// SecurityScheme is one entry of components/securitySchemes.
//
// The generator never verifies a credential itself: it works out where the
// credential lives, pulls it out, and hands it to code you write. What a token
// means is not something a specification can say.
type SecurityScheme struct {
	// Name is the key the scheme was declared under, which is how a security
	// requirement refers to it and how the generated method is named.
	Name        string
	Type        string
	Description string

	// In and ParamName locate an apiKey: header, query, or cookie, and the
	// name to read there.
	In        string
	ParamName string

	// Scheme is the HTTP authentication scheme for type: http -- bearer,
	// basic, or anything else registered with IANA.
	Scheme       string
	BearerFormat string

	Pos pos
}

// Bearer reports whether the credential arrives as an Authorization header
// carrying a bearer token. OAuth2 and OpenID Connect both land here: whatever
// the flow, what reaches the server is a bearer token.
func (s *SecurityScheme) Bearer() bool {
	switch s.Type {
	case SchemeOAuth2, SchemeOpenIDConnect:
		return true
	case SchemeHTTP:
		return strings.EqualFold(s.Scheme, "bearer")
	}
	return false
}

// Basic reports whether the credential is HTTP basic authentication.
func (s *SecurityScheme) Basic() bool {
	return s.Type == SchemeHTTP && strings.EqualFold(s.Scheme, "basic")
}

// Scoped reports whether a requirement may ask this scheme for scopes.
func (s *SecurityScheme) Scoped() bool {
	return s.Type == SchemeOAuth2 || s.Type == SchemeOpenIDConnect
}

// Challenge is the WWW-Authenticate value a 401 carries for this scheme, or
// empty when the scheme defines none. RFC 9110 requires the header on a 401,
// and a client that gets no challenge cannot know what to send next.
func (s *SecurityScheme) Challenge() string {
	switch {
	case s.Basic():
		return "Basic"
	case s.Bearer():
		return "Bearer"
	case s.Type == SchemeHTTP && s.Scheme != "":
		return strings.ToUpper(s.Scheme[:1]) + s.Scheme[1:]
	}
	return ""
}

// Requirement is one entry of a security list: every scheme in it must be
// satisfied. Separate requirements are alternatives, tried in order.
//
// A requirement with no schemes is the empty object OpenAPI uses to mean
// "anonymous is also allowed", and always succeeds.
type Requirement struct {
	Schemes []SchemeRef
	Pos     pos
}

// SchemeRef names a scheme and the scopes one operation asks of it.
type SchemeRef struct {
	Name   string
	Scopes []string
}

// Optional reports whether this requirement admits an unauthenticated caller.
func (r Requirement) Optional() bool { return len(r.Schemes) == 0 }

// Anonymous reports whether any requirement admits a caller who presents
// nothing. Such an operation still runs its authenticator, so a caller who
// does present a credential is identified rather than ignored; it just cannot
// be refused for presenting none.
func (o *Operation) Anonymous() bool {
	for _, req := range o.Security {
		if req.Optional() {
			return true
		}
	}
	return false
}

// securitySchemes parses components/securitySchemes in document order.
func (l *Loader) securitySchemes(
	file string, n *yaml.Node,
) ([]*SecurityScheme, error) {
	var out []*SecurityScheme
	err := mapEach(n, func(name string, keyNode, val *yaml.Node) error {
		file, val, _, err := l.deref(file, val)
		if err != nil {
			return err
		}
		s := &SecurityScheme{Name: name, Pos: at(file, keyNode)}

		f := &fields{file: file}
		s.Type = f.str(mapGet(val, "type"))
		s.Description = f.str(mapGet(val, "description"))
		s.In = f.str(mapGet(val, "in"))
		s.ParamName = f.str(mapGet(val, "name"))
		s.Scheme = f.str(mapGet(val, "scheme"))
		s.BearerFormat = f.str(mapGet(val, "bearerFormat"))
		if f.err != nil {
			return f.err
		}
		if err := s.validate(); err != nil {
			return err
		}
		out = append(out, s)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("components securitySchemes: %w", err)
	}
	return out, nil
}

// validate rejects a scheme the generator could not extract a credential for,
// naming what is missing rather than emitting code that reads an empty header.
func (s *SecurityScheme) validate() error {
	switch s.Type {
	case "":
		return errf(s.Pos, "securityScheme %q: type is required", s.Name)
	case SchemeAPIKey:
		switch s.In {
		case "header", "query", "cookie":
		case "":
			return errf(s.Pos,
				"securityScheme %q: apiKey requires in: header, query, or "+
					"cookie", s.Name)
		default:
			return errf(s.Pos,
				"securityScheme %q: in %q is not header, query, or cookie",
				s.Name, s.In)
		}
		if s.ParamName == "" {
			return errf(s.Pos,
				"securityScheme %q: apiKey requires the name it is read under",
				s.Name)
		}
	case SchemeHTTP:
		if s.Scheme == "" {
			return errf(s.Pos,
				"securityScheme %q: http requires scheme, such as bearer",
				s.Name)
		}
	case SchemeOAuth2, SchemeOpenIDConnect, SchemeMutualTLS:
	default:
		return errf(s.Pos,
			"securityScheme %q: unknown type %q (known: %s, %s, %s, %s, %s)",
			s.Name, s.Type, SchemeAPIKey, SchemeHTTP, SchemeOAuth2,
			SchemeOpenIDConnect, SchemeMutualTLS)
	}
	return nil
}

// securityList parses a security list: a sequence of requirement objects.
func securityList(file string, n *yaml.Node) ([]Requirement, error) {
	var out []Requirement
	err := seqEach(n, func(_ int, item *yaml.Node) error {
		req := Requirement{Pos: at(file, item)}
		err := mapEach(item, func(
			name string, keyNode, val *yaml.Node,
		) error {
			scopes, err := strsVal(file, val)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			req.Schemes = append(req.Schemes, SchemeRef{
				Name: name, Scopes: scopes,
			})
			return nil
		})
		if err != nil {
			return err
		}
		out = append(out, req)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("security: %w", err)
	}
	return out, nil
}

// resolveSecurity settles each operation's effective requirements and checks
// that every scheme named actually exists.
//
// An operation that declares no security key inherits the document's. One that
// declares an empty list overrides it, which is how a login endpoint opts out
// of the authentication it is supposed to produce.
func (d *Document) resolveSecurity() error {
	declared := map[string]*SecurityScheme{}
	for _, s := range d.SecuritySchemes {
		declared[s.Name] = s
	}

	check := func(reqs []Requirement, where string) error {
		for _, req := range reqs {
			for _, ref := range req.Schemes {
				scheme, ok := declared[ref.Name]
				if !ok {
					return errf(req.Pos,
						"%s: security scheme %q is not declared under "+
							"components/securitySchemes (declared: %s)",
						where, ref.Name, schemeNames(declared))
				}
				if len(ref.Scopes) > 0 && !scheme.Scoped() {
					return errf(req.Pos,
						"%s: security scheme %q is %s, which takes no "+
							"scopes, but %d were asked of it",
						where, ref.Name, scheme.Type, len(ref.Scopes))
				}
			}
		}
		return nil
	}

	if err := check(d.Security, "security"); err != nil {
		return err
	}
	for _, pi := range d.Paths {
		for _, op := range pi.Operations {
			if !op.HasSecurity {
				op.Security = d.Security
				continue
			}
			if err := check(op.Security, op.OperationID); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemeNames(m map[string]*SecurityScheme) string {
	if len(m) == 0 {
		return "none"
	}
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// SecurityScheme returns the scheme declared under a name.
func (d *Document) SecurityScheme(name string) *SecurityScheme {
	for _, s := range d.SecuritySchemes {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// Secured reports whether any operation in the document requires a credential.
func (d *Document) Secured() bool {
	for _, pi := range d.Paths {
		for _, op := range pi.Operations {
			if op.Secured() {
				return true
			}
		}
	}
	return false
}

// Secured reports whether the operation has credentials to check at all. A
// list made only of empty requirements has none, so no authorization is
// generated for it.
func (o *Operation) Secured() bool {
	for _, req := range o.Security {
		if !req.Optional() {
			return true
		}
	}
	return false
}

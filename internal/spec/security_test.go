package spec

import (
	"path/filepath"
	"strings"
	"testing"
)

const securedSpec = `
openapi: 3.1.0
info: { title: Secured, version: "1" }
security:
  - bearerAuth: []
paths:
  /me:
    get:
      operationId: getMe
      responses: { "200": { description: ok } }
  /login:
    post:
      operationId: login
      security: []
      responses: { "200": { description: ok } }
  /admin:
    get:
      operationId: getAdmin
      security:
        - apiKeyAuth: []
          basicAuth: []
        - oauth: [admin]
      responses: { "200": { description: ok } }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer, bearerFormat: JWT }
    basicAuth: { type: http, scheme: basic }
    apiKeyAuth: { type: apiKey, in: header, name: X-API-Key }
    unused: { type: apiKey, in: cookie, name: session }
    oauth:
      type: oauth2
      flows:
        clientCredentials:
          tokenUrl: https://example.com/token
          scopes: { admin: Everything }
`

func securedDoc(t *testing.T) *Document {
	t.Helper()
	dir := tree(t, map[string]string{"a.yaml": securedSpec})
	d, err := NewLoader().Document(filepath.Join(dir, "a.yaml"))
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	return d
}

func opByID(t *testing.T, d *Document, id string) *Operation {
	t.Helper()
	for _, pi := range d.Paths {
		for _, op := range pi.Operations {
			if op.OperationID == id {
				return op
			}
		}
	}
	t.Fatalf("operation %q not found", id)
	return nil
}

func TestSecuritySchemesParse(t *testing.T) {
	d := securedDoc(t)

	if len(d.SecuritySchemes) != 5 {
		t.Fatalf("parsed %d schemes, want 5", len(d.SecuritySchemes))
	}
	bearer := d.SecurityScheme("bearerAuth")
	if bearer == nil || !bearer.Bearer() || bearer.BearerFormat != "JWT" {
		t.Errorf("bearerAuth = %+v, want a JWT bearer scheme", bearer)
	}
	if !d.SecurityScheme("basicAuth").Basic() {
		t.Error("basicAuth should report itself as basic")
	}
	key := d.SecurityScheme("apiKeyAuth")
	if key.In != "header" || key.ParamName != "X-API-Key" {
		t.Errorf("apiKeyAuth = %+v, want the X-API-Key header", key)
	}
	// OAuth2 arrives as a bearer token whatever the flow, and takes scopes.
	oauth := d.SecurityScheme("oauth")
	if !oauth.Bearer() || !oauth.Scoped() {
		t.Errorf("oauth = %+v, want a scoped bearer scheme", oauth)
	}
	if !d.Secured() {
		t.Error("the document requires credentials somewhere")
	}
}

// An operation with no security key inherits the document's; one with an empty
// list overrides it, which is how a login endpoint opts out of the
// authentication it exists to produce.
func TestSecurityInheritanceAndOptOut(t *testing.T) {
	d := securedDoc(t)

	me := opByID(t, d, "getMe")
	if !me.Secured() || len(me.Security) != 1 ||
		me.Security[0].Schemes[0].Name != "bearerAuth" {
		t.Errorf("getMe security = %+v, want the inherited bearerAuth",
			me.Security)
	}

	login := opByID(t, d, "login")
	if login.Secured() {
		t.Error("login declared security: [] and must need nothing")
	}
	if !login.HasSecurity {
		t.Error("login wrote the key, so it overrides rather than inherits")
	}

	admin := opByID(t, d, "getAdmin")
	if len(admin.Security) != 2 {
		t.Fatalf("getAdmin has %d requirements, want 2", len(admin.Security))
	}
	// Two schemes in one requirement are an AND; two requirements are an OR.
	if len(admin.Security[0].Schemes) != 2 {
		t.Errorf("first requirement = %+v, want apiKeyAuth and basicAuth",
			admin.Security[0])
	}
	if got := admin.Security[1].Schemes[0].Scopes; len(got) != 1 ||
		got[0] != "admin" {
		t.Errorf("oauth scopes = %v, want [admin]", got)
	}
}

// An empty requirement object is how OpenAPI says an anonymous caller is also
// allowed, so it must not be mistaken for a requirement nobody can satisfy.
func TestEmptyRequirementIsOptional(t *testing.T) {
	spec := `
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /a:
    get:
      operationId: a
      security:
        - {}
        - bearerAuth: []
      responses: { "200": { description: ok } }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
`
	dir := tree(t, map[string]string{"a.yaml": spec})
	d, err := NewLoader().Document(filepath.Join(dir, "a.yaml"))
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	op := opByID(t, d, "a")
	if !op.Security[0].Optional() {
		t.Error("the empty requirement should report itself optional")
	}
	if !op.Anonymous() {
		t.Error("an empty requirement admits a caller who presents nothing")
	}
	// It still has a credential worth checking, so authorization is generated:
	// a caller who does send a token gets identified by it.
	if !op.Secured() {
		t.Error("bearerAuth is still worth trying before falling through")
	}
}

func TestSecurityErrors(t *testing.T) {
	tests := []struct{ name, spec, want string }{{
		name: "requirement names an undeclared scheme",
		spec: `
paths:
  /a:
    get:
      operationId: a
      security: [{ nope: [] }]
      responses: { "200": { description: ok } }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
`,
		want: `security scheme "nope" is not declared`,
	}, {
		name: "scopes asked of a scheme that takes none",
		spec: `
paths:
  /a:
    get:
      operationId: a
      security: [{ bearerAuth: [read] }]
      responses: { "200": { description: ok } }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
`,
		want: "takes no scopes",
	}, {
		name: "apiKey without a location",
		spec: `
paths:
  /a:
    get: { operationId: a, responses: { "200": { description: ok } } }
components:
  securitySchemes:
    k: { type: apiKey, name: X-Key }
`,
		want: "apiKey requires in: header, query, or cookie",
	}, {
		name: "apiKey without a name",
		spec: `
paths:
  /a:
    get: { operationId: a, responses: { "200": { description: ok } } }
components:
  securitySchemes:
    k: { type: apiKey, in: header }
`,
		want: "apiKey requires the name it is read under",
	}, {
		name: "http without a scheme",
		spec: `
paths:
  /a:
    get: { operationId: a, responses: { "200": { description: ok } } }
components:
  securitySchemes:
    k: { type: http }
`,
		want: "http requires scheme",
	}, {
		name: "unknown scheme type",
		spec: `
paths:
  /a:
    get: { operationId: a, responses: { "200": { description: ok } } }
components:
  securitySchemes:
    k: { type: magic }
`,
		want: `unknown type "magic"`,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tree(t, map[string]string{"a.yaml": tt.spec})
			_, err := NewLoader().Document(filepath.Join(dir, "a.yaml"))
			if err == nil {
				t.Fatal("Document succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v\nwant it to contain %q", err, tt.want)
			}
		})
	}
}

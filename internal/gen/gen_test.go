package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/franco-grobler/go-api-gen/internal/config"
	"github.com/franco-grobler/go-api-gen/internal/workspace"
)

// generate writes a workspace to a temp dir, runs the generator, and returns
// the output keyed by path relative to the module root.
func generate(
	t *testing.T,
	files map[string]string,
) (map[string]string, error) {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module github.com/acme/monorepo\n\ngo 1.24\n"
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := config.Load(filepath.Join(dir, "go-api-gen.yaml"))
	if err != nil {
		return nil, err
	}
	ws, err := workspace.Load(cfg)
	if err != nil {
		return nil, err
	}
	g := New(ws)
	if err := g.Run(); err != nil {
		return nil, err
	}

	out := map[string]string{}
	for path, data := range g.Files() {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.ToSlash(rel)] = string(data)
	}
	return out, nil
}

// oneService is a config with a single service and no shared root.
func oneService(spec string) map[string]string {
	return map[string]string{
		"go-api-gen.yaml": "version: 1\n" +
			"services:\n" +
			"  api:\n" +
			"    spec: api.yaml\n" +
			"    to: gen/api\n" +
			"    generate: [types]\n",
		"api.yaml": spec,
	}
}

// pathsFor wraps component definitions in a spec whose single operation
// reaches every one of them, since unreached schemas are tree-shaken away.
func pathsFor(components string, reach ...string) string {
	var refs strings.Builder
	for _, name := range reach {
		refs.WriteString("\n                - $ref: " +
			"'#/components/schemas/" + name + "'")
	}
	return `
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /x:
    get:
      operationId: x
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                allOf:` + refs.String() + `
components:
  schemas:
` + components
}

func TestOptionalWrappingRules(t *testing.T) {
	out, err := generate(t, oneService(pathsFor(`    S:
      type: object
      required: [req, reqNullable]
      properties:
        req: { type: string }
        reqNullable: { type: [string, "null"] }
        opt: { type: string }
        optNullable: { type: [string, "null"] }
`, "S")))
	if err != nil {
		t.Fatal(err)
	}
	src := out["gen/api/types.gen.go"]

	// A plain Go type is used only when the field is always present and can
	// never be null; every other combination has to be Optional.
	tests := []struct{ field, want string }{
		{"Req", "Req string `json:\"req\"`"},
		{"ReqNullable", "ReqNullable optional.Optional[string] " +
			"`json:\"reqNullable,omitzero\"`"},
		{"Opt", "Opt optional.Optional[string] `json:\"opt,omitzero\"`"},
		{"OptNullable", "OptNullable optional.Optional[string] " +
			"`json:\"optNullable,omitzero\"`"},
	}
	for _, tt := range tests {
		if !strings.Contains(normalize(src), normalize(tt.want)) {
			t.Errorf("field %s: want %q in\n%s", tt.field, tt.want, src)
		}
	}
}

func TestAllOfEmbedsNamedMembers(t *testing.T) {
	out, err := generate(t, oneService(pathsFor(`    Animal:
      type: object
      required: [name]
      properties:
        name: { type: string }
    Dog:
      allOf:
        - $ref: '#/components/schemas/Animal'
        - type: object
          required: [bark]
          properties:
            bark: { type: string }
`, "Dog")))
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/types.gen.go"])

	// A named allOf member is embedded, so there is still exactly one Go type
	// per schema rather than a copy of its fields.
	if !strings.Contains(src, "type Dog struct { Animal") {
		t.Errorf("Dog should embed Animal:\n%s", out["gen/api/types.gen.go"])
	}
	if !strings.Contains(src, "Bark string") {
		t.Errorf(
			"Dog should carry the inline member's fields:\n%s",
			out["gen/api/types.gen.go"],
		)
	}
	if strings.Count(src, "Name string") != 1 {
		t.Errorf(
			"Animal's field was copied into Dog instead of embedded:\n%s",
			out["gen/api/types.gen.go"],
		)
	}
}

func TestInlineObjectIsSynthesized(t *testing.T) {
	out, err := generate(t, oneService(pathsFor(`    Order:
      type: object
      required: [shipTo, lines]
      properties:
        shipTo:
          type: object
          required: [city]
          properties:
            city: { type: string }
        lines:
          type: array
          items:
            type: object
            required: [sku]
            properties:
              sku: { type: string }
`, "Order")))
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/types.gen.go"])

	// An inline object is named after where it appears, so the name is stable
	// and traceable back to the spec.
	for _, want := range []string{
		"ShipTo OrderShipTo",
		"type OrderShipTo struct",
		"Lines []OrderLinesItem",
		"type OrderLinesItem struct",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("want %q in\n%s", want, out["gen/api/types.gen.go"])
		}
	}
}

func TestEnumGeneratesConstantsAndValid(t *testing.T) {
	out, err := generate(t, oneService(pathsFor(`    Status:
      type: string
      enum: [active, pending_verification]
`, "Status")))
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/types.gen.go"])
	for _, want := range []string{
		"type Status string",
		`StatusActive Status = "active"`,
		`StatusPendingVerification Status = "pending_verification"`,
		"func (v Status) Valid() bool",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("want %q in\n%s", want, out["gen/api/types.gen.go"])
		}
	}
}

// A named scalar over an imported type must be an alias; defining it would
// discard the underlying type's methods and JSON encoding.
func TestNamedScalarAliasesImportedTypes(t *testing.T) {
	out, err := generate(t, oneService(pathsFor(
		"    Timestamp: { type: string, format: date-time }\n"+
			"    Slug: { type: string }\n",
		"Timestamp", "Slug")))
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/types.gen.go"])
	if !strings.Contains(src, "type Timestamp = time.Time") {
		t.Errorf(
			"Timestamp should alias time.Time:\n%s",
			out["gen/api/types.gen.go"],
		)
	}
	if !strings.Contains(src, "type Slug string") {
		t.Errorf(
			"Slug should be a defined type:\n%s",
			out["gen/api/types.gen.go"],
		)
	}
}

func TestOneOfIsRejectedClearly(t *testing.T) {
	_, err := generate(t, oneService(pathsFor(`    Pet:
      oneOf:
        - { type: object, properties: { bark: { type: string } } }
        - { type: object, properties: { meow: { type: string } } }
`, "Pet")))
	if err == nil {
		t.Fatal("generation succeeded, want an unsupported-feature error")
	}
	wants := []string{"oneOf/anyOf", "not supported yet", "x-go-type"}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v\nwant it to mention %q", err, want)
		}
	}
}

// The premise of the tool, at the level of emitted Go: a shared schema is
// imported by both services and declared by neither.
func TestSharedTypesAreImportedNotCopied(t *testing.T) {
	out, err := generate(t, map[string]string{
		"go-api-gen.yaml": `
version: 1
shared:
  apitypes:
    from: schema
    to: gen/apitypes
services:
  billing:
    spec: billing.yaml
    to: gen/billing
    generate: [types]
  identity:
    spec: identity.yaml
    to: gen/identity
    generate: [types]
`,
		"schema/user.yaml": "components:\n  schemas:\n    User: { type: " +
			"object, required: [id], properties: { id: { type: string } } }\n",
		"billing.yaml": `
openapi: 3.1.0
paths:
  /i:
    get:
      operationId: i
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Invoice' }
components:
  schemas:
    Invoice:
      type: object
      required: [payer]
      properties:
        payer: { $ref: schema/user.yaml#/components/schemas/User }
`,
		"identity.yaml": `
openapi: 3.1.0
paths:
  /u:
    get:
      operationId: u
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: schema/user.yaml#/components/schemas/User }
`,
	})
	if err != nil {
		t.Fatal(err)
	}

	shared := out["gen/apitypes/types.gen.go"]
	if !strings.Contains(shared, "type User struct") {
		t.Errorf("the shared root should declare User:\n%s", shared)
	}

	billing := out["gen/billing/types.gen.go"]
	if strings.Contains(billing, "type User struct") {
		t.Errorf(
			"billing redeclared the shared User instead of importing it:\n%s",
			billing,
		)
	}
	if !strings.Contains(billing, `"github.com/acme/monorepo/gen/apitypes"`) {
		t.Errorf("billing should import the shared package:\n%s", billing)
	}
	if !strings.Contains(normalize(billing), "Payer apitypes.User") {
		t.Errorf("billing should reference apitypes.User:\n%s", billing)
	}

	// Identity's every type is shared, so it declares nothing and no file is
	// written for it at all.
	if src, ok := out["gen/identity/types.gen.go"]; ok {
		t.Errorf("identity should emit no types file, got:\n%s", src)
	}
}

// normalize collapses whitespace so assertions do not depend on gofmt's
// column alignment, which shifts as unrelated fields are added.
func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// The errors block is parsed and validated by config; this is the check that
// it also arrives somewhere, rather than being a setting every binary has to
// repeat by hand.
func TestErrorsConfigReachesTheServer(t *testing.T) {
	files := oneService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /x:
    get:
      operationId: x
      responses:
        "200": { description: ok }
`)
	files["go-api-gen.yaml"] = "version: 1\n" +
		"errors:\n" +
		"  type_base_url: https://errors.acme.io/\n" +
		"  validation_title: Invalid request\n" +
		"services:\n" +
		"  api:\n" +
		"    spec: api.yaml\n" +
		"    to: gen/api\n" +
		"    generate: [types, server]\n"

	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/server.gen.go"])

	for _, want := range []string{
		`TypeBaseURL: "https://errors.acme.io/"`,
		`ValidationTitle: "Invalid request"`,
		"WithErrorOptions(DefaultErrorOptions())",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("server should contain %q:\n%s", want, src)
		}
	}
}

// With nothing configured the accessor is still emitted, so a caller always
// has one place to start from, and it carries no settings of its own.
func TestErrorsConfigDefaultsToEmptyOptions(t *testing.T) {
	files := oneService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /x:
    get:
      operationId: x
      responses:
        "200": { description: ok }
`)
	files["go-api-gen.yaml"] = "version: 1\n" +
		"services:\n" +
		"  api:\n" +
		"    spec: api.yaml\n" +
		"    to: gen/api\n" +
		"    generate: [types, server]\n"

	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/server.gen.go"])
	if !strings.Contains(src, "func DefaultErrorOptions() apierr.Options {") {
		t.Errorf("DefaultErrorOptions should always be emitted:\n%s", src)
	}
	if strings.Contains(src, "TypeBaseURL") {
		t.Errorf("nothing was configured, so nothing should be set:\n%s", src)
	}
}

// A required field the client never sent is reported as missing and not
// measured again: its other constraints are skipped, so one absent field
// produces one error rather than one per keyword.
func TestAbsentRequiredFieldSkipsItsOtherChecks(t *testing.T) {
	files := oneService(pathsFor(`    S:
      type: object
      required: [name, size]
      properties:
        name: { type: string, minLength: 1 }
        nickname: { type: string, minLength: 1 }
        size: { $ref: '#/components/schemas/Size' }
    Size:
      type: object
      required: [width]
      properties:
        width: { type: integer, minimum: 1 }
`, "S"))
	files["go-api-gen.yaml"] = "version: 1\n" +
		"services:\n" +
		"  api:\n" +
		"    spec: api.yaml\n" +
		"    to: gen/api\n" +
		"    generate: [types, validate]\n"

	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/validate.gen.go"])

	guard := func(field string) string {
		return "if !validate.Absent(s.missingFields, " + strconv.Quote(field) +
			") {"
	}
	// A required scalar guards its own keywords.
	if !strings.Contains(src, guard("name")) {
		t.Errorf("a required field's checks should be guarded:\n%s", src)
	}
	// A required object guards the recursion too, or an absent one would be
	// walked as a zero value and report its own contents as invalid.
	if !strings.Contains(src, guard("size")) {
		t.Errorf("a required object's Validate call should be guarded:\n%s",
			src)
	}
	// An optional field is already guarded by its Optional[T].
	if strings.Contains(src, guard("nickname")) {
		t.Errorf("an optional field needs no missing guard:\n%s", src)
	}
}

// securedService is a workspace whose spec declares security, generating the
// server as well as the types so the Authenticator surface is emitted.
func securedService(spec string) map[string]string {
	return map[string]string{
		"go-api-gen.yaml": "version: 1\n" +
			"services:\n" +
			"  api:\n" +
			"    spec: api.yaml\n" +
			"    to: gen/api\n" +
			"    generate: [types, validate, server]\n",
		"api.yaml": spec,
	}
}

const securedSpec = `
openapi: 3.1.0
info: { title: T, version: "1" }
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
          bearerAuth: []
        - oauth: [admin]
      responses: { "200": { description: ok } }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
    apiKeyAuth: { type: apiKey, in: header, name: X-API-Key }
    unused: { type: apiKey, in: cookie, name: session }
    oauth:
      type: oauth2
      flows:
        clientCredentials:
          tokenUrl: https://example.com/token
          scopes: { admin: Everything }
`

func TestSecurityGeneratesAnAuthenticator(t *testing.T) {
	out, err := generate(t, securedService(securedSpec))
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/server.gen.go"])

	// One method per scheme an operation actually names, with the credential
	// the specification says arrives rather than a bag of strings.
	for _, want := range []string{
		"BearerAuth(ctx context.Context, r *http.Request, token string)",
		"APIKeyAuth(ctx context.Context, r *http.Request, key string)",
		"Oauth(ctx context.Context, r *http.Request, token string, " +
			"scopes []string)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("Authenticator should declare %q:\n%s", want, src)
		}
	}
	// A scheme nobody requires is not a method somebody has to write.
	if strings.Contains(src, "Unused(ctx") {
		t.Errorf("an unreferenced scheme should generate no method:\n%s", src)
	}

	// The authenticator is a parameter, so a server cannot be built without
	// one when the specification asks for it.
	if !strings.Contains(src,
		"func NewServer(h Handler, a Authenticator, opts ...ServerOption)") {
		t.Errorf("NewServer should require an Authenticator:\n%s", src)
	}

	// security: [] overrides the document default rather than inheriting it.
	if strings.Contains(src, "func authorizeLogin(") {
		t.Errorf("an opted-out operation needs no authorization:\n%s", src)
	}
	if !strings.Contains(src, "func authorizeGetMe(") {
		t.Errorf("an inheriting operation should be authorized:\n%s", src)
	}

	// Two schemes in one requirement are an AND, in one authAll chain; two
	// requirements are an OR, in two.
	admin := section(t, out["gen/api/server.gen.go"], "func authorizeGetAdmin(")
	if n := strings.Count(admin, "authAll(a, r"); n != 2 {
		t.Errorf("getAdmin should try 2 requirements, found %d:\n%s", n, admin)
	}
	if !strings.Contains(normalize(admin), "authStep{authOauth, "+
		`[]string{"admin"}}`) {
		t.Errorf("the operation's scopes should reach the step:\n%s", admin)
	}
}

// An empty requirement means a caller may present nothing, but one who does
// present a credential is still identified by it.
func TestAnonymousRequirementFallsThrough(t *testing.T) {
	out, err := generate(t, securedService(`
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
`))
	if err != nil {
		t.Fatal(err)
	}
	body := section(t, out["gen/api/server.gen.go"], "func authorizeA(")
	if !strings.Contains(body, "authStep{authBearerAuth") {
		t.Errorf("the credential should still be tried:\n%s", body)
	}
	if !strings.Contains(body, "return r.Context(), nil") {
		t.Errorf("a caller with no credential should continue:\n%s", body)
	}
}

// A specification with no security generates none of the machinery, so the
// server keeps the signature every unsecured service has.
func TestNoSecurityGeneratesNoAuthenticator(t *testing.T) {
	out, err := generate(t, securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /a:
    get:
      operationId: a
      responses: { "200": { description: ok } }
`))
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/server.gen.go"])
	if strings.Contains(src, "Authenticator") {
		t.Errorf("no security was declared:\n%s", src)
	}
	if !strings.Contains(src,
		"func NewServer(h Handler, opts ...ServerOption)") {
		t.Errorf("NewServer should be unchanged:\n%s", src)
	}
}

// section returns the function beginning with prefix, up to the closing brace
// in the first column, so an assertion can be scoped to one function.
func section(t *testing.T, src, prefix string) string {
	t.Helper()
	i := strings.Index(src, prefix)
	if i < 0 {
		t.Fatalf("%q not found in:\n%s", prefix, src)
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// compiles builds generated output as a module of its own against the local
// runtime module, and fails the test with the compiler's output if it does
// not build. Matching on the source cannot catch code that reads correctly
// and does not compile, which is how both bugs below went unnoticed.
func compiles(t *testing.T, out map[string]string) {
	t.Helper()
	goCommand(t, out, nil, "build", "./...")
}

// passes runs extra -- hand-written files, keyed by path like out -- as Go
// tests against the generated code, for behaviour no amount of reading the
// source would prove.
func passes(t *testing.T, out, extra map[string]string) {
	t.Helper()
	goCommand(t, out, extra, "test", "./...")
}

// goCommand writes out and extra as one module beside the local runtime
// module and runs the go command on it.
func goCommand(t *testing.T, out, extra map[string]string, args ...string) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs the go toolchain on the generated code")
	}
	runtimeDir, err := filepath.Abs(filepath.Join("..", "..", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module github.com/acme/monorepo\n\ngo 1.24\n\n" +
			"require github.com/franco-grobler/go-api-gen/runtime v0.0.0\n\n" +
			"replace github.com/franco-grobler/go-api-gen/runtime => " +
			runtimeDir + "\n",
	}
	for name, body := range out {
		if strings.HasSuffix(name, ".go") {
			files[name] = body
		}
	}
	for name, body := range extra {
		files[name] = body
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, b)
	}
}

// A named scalar declared as an alias has no method set of its own, so it is
// validated by value where it is used, never through a Validate method: one
// declared on an alias of time.Time is a method on time.Time.
func TestAliasedScalarHasNoValidateMethod(t *testing.T) {
	out, err := generate(t, securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /events/{at}:
    parameters:
      - { name: at, in: path, required: true,
          schema: { $ref: '#/components/schemas/Timestamp' } }
      - { name: since, in: query,
          schema: { $ref: '#/components/schemas/Timestamp' } }
    put:
      operationId: putEvent
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/Event' }
      responses: { "204": { description: ok } }
  /stamps:
    post:
      operationId: postStamp
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/Timestamp' }
      responses: { "204": { description: ok } }
components:
  schemas:
    Timestamp: { type: string, format: date-time }
    Anything: {}
    Event:
      type: object
      required: [at]
      properties:
        at: { $ref: '#/components/schemas/Timestamp' }
        seen: { $ref: '#/components/schemas/Timestamp' }
        extra: { $ref: '#/components/schemas/Anything' }
        name: { type: string, minLength: 1 }
`))
	if err != nil {
		t.Fatal(err)
	}
	valid := normalize(out["gen/api/validate.gen.go"])
	for _, alias := range []string{"Timestamp", "Anything"} {
		if strings.Contains(valid, " "+alias+") Validate(") {
			t.Errorf("%s is an alias and cannot carry Validate:\n%s",
				alias, out["gen/api/validate.gen.go"])
		}
	}
	if !strings.Contains(valid, "func (event Event) Validate(") {
		t.Errorf("Event is a defined type and should still validate:\n%s",
			out["gen/api/validate.gen.go"])
	}
	compiles(t, out)
}

// An enum parameter that is not a component schema -- written inline, or a
// $ref to a whole file -- is declared under a synthesized name, and the raw
// string must be converted to that type rather than to its base.
func TestUnnamedEnumParameterParsesToItsType(t *testing.T) {
	files := securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /search:
    get:
      operationId: search
      parameters:
        - { name: kind, in: query, schema: { $ref: kind.yaml } }
        - { name: order, in: query, required: true,
            schema: { type: string, enum: [asc, desc] } }
      responses: { "204": { description: ok } }
`)
	files["kind.yaml"] = "type: string\nenum: [video, channel]\n"
	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	server := normalize(out["gen/api/server.gen.go"])
	for _, want := range []string{
		"req.Query.Kind = optional.Some(SearchQueryParamsKind(raw))",
		"req.Query.Order = SearchQueryParamsOrder(raw)",
	} {
		if !strings.Contains(server, want) {
			t.Errorf("want %q in\n%s", want, out["gen/api/server.gen.go"])
		}
	}
	compiles(t, out)
}

// A struct embedding a named member that decodes itself -- one with required
// fields to track -- used to inherit that member's UnmarshalJSON, so the
// whole body decoded into the member and every other field stayed zero. It
// now decodes all of them, and the member still records its own absences.
func TestEmbeddedDecoderDoesNotSwallowTheBody(t *testing.T) {
	out, err := generate(t, oneService(pathsFor(`    Base:
      type: object
      required: [id]
      properties:
        id: { type: string }
    Video:
      allOf:
        - $ref: '#/components/schemas/Base'
        - type: object
          required: [title]
          properties:
            title: { type: string }
    Note:
      allOf:
        - $ref: '#/components/schemas/Base'
        - type: object
          properties:
            text: { type: string }
`, "Video", "Note")))
	if err != nil {
		t.Fatal(err)
	}
	passes(t, out, map[string]string{
		"gen/api/decode_test.go": `package api

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestOwnFieldsDecode(t *testing.T) {
	var v Video
	if err := json.Unmarshal([]byte("{\"title\": \"t\"}"), &v); err != nil {
		t.Fatal(err)
	}
	if v.Title != "t" {
		t.Errorf("Title = %q, want \"t\"", v.Title)
	}
	if !slices.Equal(v.Base.missingFields, []string{"id"}) {
		t.Errorf("Base missing = %q, want [id]", v.Base.missingFields)
	}
	if len(v.missingFields) > 0 {
		t.Errorf("Video missing = %q, want none", v.missingFields)
	}
}

func TestEmbeddedFieldsDecode(t *testing.T) {
	var v Video
	err := json.Unmarshal([]byte("{\"id\": \"a\"}"), &v)
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "a" {
		t.Errorf("ID = %q, want \"a\"", v.ID)
	}
	if !slices.Equal(v.missingFields, []string{"title"}) {
		t.Errorf("Video missing = %q, want [title]", v.missingFields)
	}
}

// Note has no required field of its own, so it needs a decoder only to keep
// Base's from being promoted onto it.
func TestDecoderOnlyForTheMember(t *testing.T) {
	var n Note
	err := json.Unmarshal([]byte("{\"id\": \"a\", \"text\": \"x\"}"), &n)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := n.Text.Get(); n.ID != "a" || got != "x" {
		t.Errorf("Note = {ID: %q, Text: %q}, want {a, x}", n.ID, got)
	}
}
`,
	})
}

// A property whose only constraint is a custom rule is checked by the rule,
// which its owner calls as a method. Unwrapping the Optional anyway left an
// empty block around an unused variable, which does not compile.
func TestOptionalPropertyWithOnlyACustomRule(t *testing.T) {
	files := oneService(pathsFor(`    S:
      type: object
      properties:
        slug: { type: string, x-validate: [lowercase] }
`, "S"))
	files["go-api-gen.yaml"] = strings.Replace(files["go-api-gen.yaml"],
		"generate: [types]", "generate: [types, validate]", 1)
	files["gen/api/rules.go"] = "package api\n\n" +
		"import \"github.com/franco-grobler/go-api-gen/runtime/validate\"\n\n" +
		"func (S) ValidateSlugLowercase(*validate.V) {}\n"

	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	src := normalize(out["gen/api/validate.gen.go"])
	if strings.Contains(src, "s.Slug.Get()") {
		t.Errorf("a rule-only property needs no unwrap:\n%s", src)
	}
	if !strings.Contains(src, `s.ValidateSlugLowercase(v.Field("slug"))`) {
		t.Errorf("the rule should still be called:\n%s", src)
	}
	out["gen/api/rules.go"] = files["gen/api/rules.go"]
	compiles(t, out)
}

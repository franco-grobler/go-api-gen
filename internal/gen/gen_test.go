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
	files["go.mod"] = "module github.com/acme/monorepo\n\ngo 1.27.1\n"
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
			"`json:\"reqNullable\"`"},
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

// A required nullable property must always be sent, so one a handler left
// unset encodes as null rather than being omitted, while an unset optional
// property is still left out.
func TestUnsetRequiredNullableEncodesAsNull(t *testing.T) {
	out, err := generate(t, oneService(pathsFor(`    S:
      type: object
      required: [name]
      properties:
        name: { type: [string, "null"] }
        note: { type: [string, "null"] }
`, "S")))
	if err != nil {
		t.Fatal(err)
	}
	passes(t, out, map[string]string{
		"gen/api/encode_test.go": `package api

import (
	"encoding/json"
	"testing"
)

func TestUnsetRequiredNullable(t *testing.T) {
	b, err := json.Marshal(S{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), "{\"name\":null}"; got != want {
		t.Errorf("Marshal(S{}) = %s, want %s", got, want)
	}
}
`,
	})
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

// A union of inline objects, which have no names of their own, still gets
// a field per variant, named for the kind of value it holds.
func TestInlineUnionMembersAreNamedByKind(t *testing.T) {
	out, err := generate(t, oneService(pathsFor(`    Pet:
      oneOf:
        - type: object
          required: [bark]
          properties: { bark: { type: string } }
        - type: object
          required: [meow]
          properties: { meow: { type: string } }
`, "Pet")))
	if err != nil {
		t.Fatal(err)
	}
	types := normalize(out["gen/api/types.gen.go"])
	if !strings.Contains(types,
		"type Pet struct { Object *PetObject Object2 *PetObject2") {
		t.Errorf("want a field per inline member:\n%s", types)
	}
	compiles(t, out)
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
		"go.mod": "module github.com/acme/monorepo\n\ngo 1.27.1\n\n" +
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

// A property declared in an inline allOf member is a field of the composed
// struct like any other, so its constraints and custom rules are checked
// like any other's. They used to be generated and then never run.
func TestInlineAllOfMemberIsValidated(t *testing.T) {
	files := oneService(pathsFor(`    Base:
      type: object
      required: [id]
      properties:
        id: { type: string, minLength: 1 }
    Video:
      allOf:
        - $ref: '#/components/schemas/Base'
        - type: object
          required: [title]
          properties:
            title: { type: string, maxLength: 5 }
            rating: { type: integer, minimum: 0, maximum: 5 }
            slug: { type: string, x-validate: [lowercase] }
`, "Video"))
	files["go-api-gen.yaml"] = strings.Replace(files["go-api-gen.yaml"],
		"generate: [types]", "generate: [types, validate]", 1)

	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	passes(t, out, map[string]string{
		"gen/api/rules.go": `package api

import (
	"strings"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

func (x Video) ValidateSlugLowercase(v *validate.V) {
	if s, ok := x.Slug.Get(); ok && s != strings.ToLower(s) {
		v.Fail("lowercase", "Slug should be lowercase")
	}
}
`,
		"gen/api/member_test.go": `package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

func failures(t *testing.T, body string) []string {
	t.Helper()
	var x Video
	if err := json.Unmarshal([]byte(body), &x); err != nil {
		t.Fatal(err)
	}
	v := validate.New()
	x.Validate(v)
	var got []string
	for _, e := range v.Errors() {
		got = append(got, fmt.Sprint(e.Loc...)+": "+e.Type)
	}
	slices.Sort(got)
	return got
}

func TestMemberConstraintsAreChecked(t *testing.T) {
	got := failures(t, "{\"id\": \"\", \"title\": \"too long\", "+
		"\"rating\": 9, \"slug\": \"ABC\"}")
	want := []string{
		"id: string_too_short",
		"rating: less_than_equal",
		"slug: lowercase",
		"title: string_too_long",
	}
	if !slices.Equal(got, want) {
		t.Errorf("failures = %q, want %q", got, want)
	}
}

func TestMissingMemberFieldIsReportedOnce(t *testing.T) {
	got := failures(t, "{\"id\": \"a\"}")
	if want := []string{"title: missing"}; !slices.Equal(got, want) {
		t.Errorf("failures = %q, want %q", got, want)
	}
}

func TestValidMemberFieldsPass(t *testing.T) {
	got := failures(t, "{\"id\": \"a\", \"title\": \"short\", "+
		"\"rating\": 5, \"slug\": \"abc\"}")
	if len(got) > 0 {
		t.Errorf("unexpected failures %q", got)
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

// allOf of a single $ref plus a description is how OpenAPI 3.0 describes a
// reference, and it means the referenced schema. It used to be typed as a
// struct embedding its member, which over a whole-file enum was an empty
// struct that no JSON string decodes into.
func TestAnnotatedRefIsItsMember(t *testing.T) {
	files := securedService(`
openapi: 3.0.3
info: { title: T, version: "1" }
paths:
  /reindex:
    post:
      operationId: reindex
      parameters:
        - name: scope
          in: query
          schema:
            description: Restrict the rebuild.
            allOf: [{ $ref: kind.yaml }]
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/Reindex' }
      responses: { "204": { description: ok } }
components:
  schemas:
    Status: { type: string, enum: [on, off] }
    Reindex:
      type: object
      required: [kind]
      properties:
        kind:
          description: Which entities to rebuild.
          allOf: [{ $ref: kind.yaml }]
        status:
          description: Whether the rebuild runs.
          allOf: [{ $ref: '#/components/schemas/Status' }]
`)
	files["kind.yaml"] = "type: string\nenum: [video, channel]\n"
	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	passes(t, out, map[string]string{
		"gen/api/wrapper_test.go": `package api

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

func decode(t *testing.T, body string) (Reindex, []string) {
	t.Helper()
	var r Reindex
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decoding %s: %v", body, err)
	}
	v := validate.New()
	r.Validate(v)
	var got []string
	for _, e := range v.Errors() {
		got = append(got, e.Type)
	}
	return r, got
}

func TestWrappedFieldsAreTheirMembers(t *testing.T) {
	r, got := decode(t, "{\"kind\": \"video\", \"status\": \"on\"}")
	if len(got) > 0 {
		t.Errorf("unexpected failures %q", got)
	}
	status, _ := r.Status.Get()
	if string(r.Kind) != "video" || status != StatusOn {
		t.Errorf("decoded %+v", r)
	}
}

func TestWrappedFieldsKeepTheirMembersRules(t *testing.T) {
	_, got := decode(t, "{\"kind\": \"film\", \"status\": \"maybe\"}")
	if want := []string{"enum", "enum"}; !slices.Equal(got, want) {
		t.Errorf("failures = %q, want %q", got, want)
	}
}
`,
	})
	server := normalize(out["gen/api/server.gen.go"])
	if strings.Contains(server, "optional.Some(string(raw))") {
		t.Errorf("a wrapped enum parameter should parse to its type:\n%s",
			out["gen/api/server.gen.go"])
	}
}

// minProperties and maxProperties count the keys of the JSON object, so a
// PATCH body of {} can be refused. A struct cannot be measured after the
// fact, since an absent field and a zero one look alike, so it counts the keys
// as it decodes; a map is measured directly.
func TestPropertyCountIsChecked(t *testing.T) {
	files := oneService(pathsFor(`    Base:
      type: object
      required: [id]
      properties:
        id: { type: string }
    Update:
      type: object
      minProperties: 1
      maxProperties: 2
      properties:
        title: { type: string }
        text: { type: string }
        tags:
          type: object
          minProperties: 1
          additionalProperties: { type: string }
    Video:
      allOf:
        - $ref: '#/components/schemas/Base'
        - type: object
          minProperties: 2
          properties:
            title: { type: string }
`, "Update", "Video"))
	files["go-api-gen.yaml"] = strings.Replace(files["go-api-gen.yaml"],
		"generate: [types]", "generate: [types, validate]", 1)

	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	passes(t, out, map[string]string{
		"gen/api/count_test.go": `package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

func failures(t *testing.T, x interface{ Validate(*validate.V) },
	body string,
) []string {
	t.Helper()
	if err := json.Unmarshal([]byte(body), x); err != nil {
		t.Fatal(err)
	}
	v := validate.New()
	x.Validate(v)
	var got []string
	for _, e := range v.Errors() {
		got = append(got, fmt.Sprint(e.Loc...)+": "+e.Type)
	}
	return got
}

func TestPropertyCount(t *testing.T) {
	for _, c := range []struct {
		body string
		want []string
	}{
		{"{}", []string{": too_short"}},
		{"{\"title\": \"t\"}", nil},
		// An unknown key is a key: JSON Schema counts it.
		{"{\"other\": 1}", nil},
		{"{\"title\": \"t\", \"text\": \"x\", \"other\": 1}",
			[]string{": too_long"}},
		{"{\"tags\": {}}", []string{"tags: too_short"}},
		{"{\"tags\": {\"a\": \"b\"}}", nil},
	} {
		got := failures(t, &Update{}, c.body)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: failures = %q, want %q", c.body, got, c.want)
		}
	}
}

// The inline member's bound counts the whole object, embedded keys included.
func TestPropertyCountThroughAllOf(t *testing.T) {
	got := failures(t, &Video{}, "{\"id\": \"a\"}")
	if want := []string{": too_short"}; !slices.Equal(got, want) {
		t.Errorf("failures = %q, want %q", got, want)
	}
	got = failures(t, &Video{}, "{\"id\": \"a\", \"title\": \"t\"}")
	if len(got) > 0 {
		t.Errorf("failures = %q, want none", got)
	}
}
`,
	})
}

// A schema named for an operation's request, as OpenAI names its bodies, used
// to be declared a second time by the server. The schema keeps its name,
// since its author chose it and other packages may import it, and the
// server's struct is the one that yields.
func TestRequestStructYieldsToASchema(t *testing.T) {
	out, err := generate(t, securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /things:
    post:
      operationId: createThing
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/CreateThingRequest' }
      responses: { "204": { description: ok } }
  /things/{id}:
    get:
      operationId: getThing
      parameters:
        - { name: id, in: path, required: true, schema: { type: string } }
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: '#/components/schemas/GetThingPathParams' }
    delete:
      operationId: deleteThing
      parameters:
        - { name: id, in: path, required: true, schema: { type: string } }
      requestBody:
        content:
          application/json:
            schema: { $ref: '#/components/schemas/DeleteThingRequest' }
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: '#/components/schemas/DeleteThingInput' }
components:
  schemas:
    CreateThingRequest:
      type: object
      properties:
        name: { type: string }
    GetThingPathParams:
      type: object
      properties:
        id: { type: string }
    DeleteThingRequest:
      type: object
      properties:
        reason: { type: string }
    DeleteThingInput:
      type: object
      properties:
        ok: { type: boolean }
`))
	if err != nil {
		t.Fatal(err)
	}
	server := normalize(out["gen/api/server.gen.go"])
	for _, want := range []string{
		"CreateThing(ctx context.Context, req CreateThingInput)",
		"type CreateThingInput struct { Body CreateThingRequest ",
		"type GetThingRequest struct { Path GetThingPathParams2",
		"type GetThingPathParams2 struct { ID string }",
		// Both words taken, so the last resort is a number.
		"DeleteThing(ctx context.Context, req DeleteThingInput2)",
	} {
		if !strings.Contains(server, want) {
			t.Errorf("want %q in\n%s", want, out["gen/api/server.gen.go"])
		}
	}
	compiles(t, out)
}

// A schema cannot take a name the server declares for itself, because those
// names are the server's API and have nowhere else to go.
func TestSchemaNamedLikeTheServerIsRejected(t *testing.T) {
	_, err := generate(t, securedService(`
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
              schema: { $ref: '#/components/schemas/Handler' }
components:
  schemas:
    Handler:
      type: object
      properties:
        name: { type: string }
`))
	if err == nil {
		t.Fatal("generation succeeded, want a name collision error")
	}
	for _, want := range []string{`"Handler"`, "x-go-name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v\nwant it to mention %s", err, want)
		}
	}
}

// A Handler method is documented with its operation's summary, description,
// and one example each way. A standard example wins over one at the
// configured path, which fills in only the side the standard leaves empty.
func TestHandlerDocHasSummaryAndExamples(t *testing.T) {
	files := securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /pets:
    post:
      operationId: createPet
      summary: Create a pet.
      description: The pet is created in the caller's store.
      requestBody:
        content:
          application/json:
            schema: { type: object, properties: { name: { type: string } } }
            examples:
              rex:
                summary: A dog
                value: { name: Rex, kind: dog }
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { type: object, properties: { id: { type: string } } }
      x-samples:
        - title: Configured
          request: '{"name": "ignored"}'
          response: |
            {"id": "pet_1", "name": "Rex"}
  /pets/{id}:
    delete:
      operationId: deletePet
      parameters:
        - { name: id, in: path, required: true, schema: { type: string } }
      responses: { "204": { description: ok } }
`)
	files["go-api-gen.yaml"] += "    examples:\n      path: x-samples\n"
	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	doc := section(t, out["gen/api/server.gen.go"], "type Handler interface")
	want := `	// CreatePet: Create a pet.
	//
	// The pet is created in the caller's store.
	//
	// Example request (A dog):
	//
	//	{
	//	  "name": "Rex",
	//	  "kind": "dog"
	//	}
	//
	// Example response (Configured):
	//
	//	{
	//	  "id": "pet_1",
	//	  "name": "Rex"
	//	}
	CreatePet(`
	if !strings.Contains(doc, want) {
		t.Errorf("want\n%s\nin\n%s", want, doc)
	}
	// An operation with nothing to say gets no comment at all.
	if strings.Contains(doc, "// DeletePet") {
		t.Errorf("an undocumented operation should have no comment:\n%s", doc)
	}
	compiles(t, out)
}

// An array or object parameter is read as its style lays it out, with
// OpenAPI's defaults: a query parameter is form and exploded, a path or
// header parameter simple and not. Each element is converted to its type and
// located by index or key when it fails.
func TestStructuredParametersDecode(t *testing.T) {
	files := securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /search/{ids}:
    get:
      operationId: search
      parameters:
        - { name: ids, in: path, required: true,
            schema: { type: array, items: { type: integer } } }
        - { name: "tags[]", in: query,
            schema: { type: array, items: { type: string, enum: [a, b] } } }
        - { name: sizes, in: query, explode: false,
            schema: { type: array, items: { type: integer } } }
        - { name: kinds, in: query, style: pipeDelimited,
            schema: { type: array, items: { type: string } } }
        - { name: metadata, in: query, style: deepObject,
            schema: { type: object,
                      additionalProperties: { type: string } } }
        - { name: range, in: query, required: true,
            schema: { type: object, required: [lo],
                      properties: { lo: { type: integer },
                                    hi: { type: integer } } } }
        - { name: point, in: query, explode: false,
            schema: { type: object,
                      properties: { x: { type: integer },
                                    y: { type: integer } } } }
        - { name: X-Trace, in: header,
            schema: { type: array, items: { type: string } } }
      responses: { "204": { description: ok } }
`)
	out, err := generate(t, files)
	if err != nil {
		t.Fatal(err)
	}
	passes(t, out, map[string]string{
		"gen/api/params_test.go": `package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type handler struct{ got SearchRequest }

func (h *handler) Search(
	_ context.Context, req SearchRequest,
) (struct{}, error) {
	h.got = req
	return struct{}{}, nil
}

func serve(
	t *testing.T, target string, header http.Header,
) (*handler, *httptest.ResponseRecorder) {
	t.Helper()
	h := &handler{}
	r := httptest.NewRequest("GET", target, nil)
	for k, vs := range header {
		r.Header[k] = vs
	}
	w := httptest.NewRecorder()
	NewServer(h).ServeHTTP(w, r)
	return h, w
}

func TestEveryStyleDecodes(t *testing.T) {
	h, w := serve(t, "/search/1,2?tags[]=a&tags[]=b&sizes=3,4&kinds=x|y"+
		"&metadata[team]=core&metadata[env]=prod&lo=1&hi=9&point=x,5,y,6",
		http.Header{"X-Trace": {"t1,t2"}})
	if w.Code != 204 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	q := h.got.Query
	if got := h.got.Path.Ids; !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("ids = %v", got)
	}
	if got, _ := q.Tags.Get(); !reflect.DeepEqual(got,
		[]SearchQueryParamsTagsItem{"a", "b"}) {
		t.Errorf("tags = %v", got)
	}
	if got, _ := q.Sizes.Get(); !reflect.DeepEqual(got, []int{3, 4}) {
		t.Errorf("sizes = %v", got)
	}
	if got, _ := q.Kinds.Get(); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("kinds = %v", got)
	}
	if got, _ := q.Metadata.Get(); !reflect.DeepEqual(got,
		map[string]string{"team": "core", "env": "prod"}) {
		t.Errorf("metadata = %v", got)
	}
	if hi, _ := q.Range.Hi.Get(); q.Range.Lo != 1 || hi != 9 {
		t.Errorf("range = %+v", q.Range)
	}
	p, _ := q.Point.Get()
	if x, _ := p.X.Get(); x != 5 {
		t.Errorf("point = %+v", p)
	}
	if got, _ := h.got.Header.XTrace.Get(); !reflect.DeepEqual(got,
		[]string{"t1", "t2"}) {
		t.Errorf("X-Trace = %v", got)
	}
}

// Every failure is reported, each where it happened, in one response.
func TestEachFailureIsLocated(t *testing.T) {
	_, w := serve(t, "/search/1,z?tags[]=a&tags[]=c&hi=x&point=x,1,y", nil)
	if w.Code != 422 {
		t.Fatalf("status %d, want 422: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{
		` + "`" + `"loc":["path","ids",1]` + "`" + `,
		` + "`" + `"loc":["query","tags[]",1]` + "`" + `,
		` + "`" + `"loc":["query","range","lo"]` + "`" + `,
		` + "`" + `"loc":["query","range","hi"]` + "`" + `,
		` + "`" + `"loc":["query","point"]` + "`" + `,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %s in %s", want, body)
		}
	}
}

// A required object with none of its keys sent is missing as a whole.
func TestAbsentObjectIsMissing(t *testing.T) {
	_, w := serve(t, "/search/1", nil)
	want := ` + "`" + `"loc":["query","range"]` + "`" + `
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("want range reported missing: %s", w.Body)
	}
}
`,
	})
}

// A shape with no defined string form is refused while generating, not left
// for the compiler to trip over.
func TestUnsupportedParameterShapeIsRejected(t *testing.T) {
	_, err := generate(t, securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /x:
    get:
      operationId: x
      parameters:
        - { name: grid, in: query,
            schema: { type: array,
                      items: { type: array, items: { type: integer } } } }
      responses: { "204": { description: ok } }
`))
	if err == nil {
		t.Fatal("generation succeeded, want an unsupported-shape error")
	}
	for _, want := range []string{`"grid"`, "not supported yet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v\nwant it to mention %s", err, want)
		}
	}
}

// x-go-type replaces whatever the schema describes, an object included: the
// schema is declared from the hand-written type, and its keywords, which
// describe that type's JSON, are not checked through a Validate method the
// type does not have.
func TestGoTypeReplacesAnObject(t *testing.T) {
	out, err := generate(t, securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /x:
    post:
      operationId: x
      requestBody:
        content:
          application/json:
            schema: { $ref: '#/components/schemas/Envelope' }
      responses: { "204": { description: ok } }
components:
  schemas:
    Raw:
      type: object
      x-go-type: json.RawMessage
      x-go-import: encoding/json
      required: [kind]
      properties:
        kind: { type: string, minLength: 1 }
    Envelope:
      type: object
      properties:
        named: { $ref: '#/components/schemas/Raw' }
        inline:
          type: object
          x-go-type: json.RawMessage
          x-go-import: encoding/json
          properties:
            kind: { type: string, minLength: 1 }
`))
	if err != nil {
		t.Fatal(err)
	}
	types := normalize(out["gen/api/types.gen.go"])
	if !strings.Contains(types, "type Raw = json.RawMessage") {
		t.Errorf("Raw should alias its Go type:\n%s", types)
	}
	if strings.Contains(types, "type EnvelopeInline struct") {
		t.Errorf("an inline x-go-type should not be synthesized:\n%s", types)
	}
	compiles(t, out)
}

// A bound no value of the Go type can cross is dropped, not emitted: it may
// not be a constant of that type, and OpenAI writes int64's range as
// ±9223372036854776000, which is ±1<<63 once parsed.
func TestUnreachableIntegerBoundIsDropped(t *testing.T) {
	out, err := generate(t, securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /x:
    post:
      operationId: x
      requestBody:
        content:
          application/json:
            schema: { $ref: '#/components/schemas/Seeded' }
      responses: { "204": { description: ok } }
components:
  schemas:
    Seeded:
      type: object
      properties:
        seed:
          type: integer
          minimum: -9223372036854776000
          maximum: 9223372036854776000
        small:
          type: integer
          format: int32
          minimum: -5000000000
          exclusiveMaximum: 2147483648
        kept:
          type: integer
          format: int32
          exclusiveMinimum: -2147483648
          maximum: 2147483646
`))
	if err != nil {
		t.Fatal(err)
	}
	v := normalize(out["gen/api/validate.gen.go"])
	for _, bad := range []string{"9223372036854775", "e+18", "5000000000",
		">= 2147483648"} {
		if strings.Contains(v, bad) {
			t.Errorf("unreachable bound %q emitted:\n%s", bad, v)
		}
	}
	for _, want := range []string{"<= -2147483648", "> 2147483646"} {
		if !strings.Contains(v, want) {
			t.Errorf("reachable bound %q missing:\n%s", want, v)
		}
	}
	compiles(t, out)
}

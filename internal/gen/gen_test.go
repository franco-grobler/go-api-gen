package gen

import (
	"os"
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

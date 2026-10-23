package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/franco-grobler/go-api-gen/internal/config"
	"github.com/franco-grobler/go-api-gen/internal/workspace"
)

// unify writes a workspace, runs the generator, and returns the unified spec
// both as text and decoded.
func unify(
	t *testing.T,
	files map[string]string,
) (text string, doc map[string]any, err error) {
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
		return "", nil, err
	}
	ws, err := workspace.Load(cfg)
	if err != nil {
		return "", nil, err
	}
	g := New(ws)
	if err := g.Run(); err != nil {
		return "", nil, err
	}
	data, err := g.Unified()
	if err != nil {
		return "", nil, err
	}

	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unified spec is not valid YAML: %v\n%s", err, data)
	}
	return string(data), doc, nil
}

// service builds a spec with the given title and path entries.
func service(title, paths string) string {
	return "openapi: 3.1.0\n" +
		"info: { title: " + title + ", version: \"1\" }\n" +
		"paths:\n" + paths
}

const unifiedConfig = `
version: 1
shared:
  apitypes:
    from: schema
    to: gen/apitypes
services:
  billing:
    spec: services/billing/api.yaml
    to: gen/billing
    generate: [types]
  identity:
    spec: services/identity/api.yaml
    to: gen/identity
    generate: [types]
unified:
  to: gen/openapi.yaml
  title: Acme Platform API
  version: "2026.1"
  servers:
    - { url: 'https://api.acme.io', description: Production }
`

func twoServices() map[string]string {
	return map[string]string{
		"go-api-gen.yaml": unifiedConfig,
		"schema/user.yaml": "components:\n  schemas:\n    User: { type: " +
			"object, required: [id], properties: { id: { type: string } } }\n",
		"services/billing/api.yaml": service("Billing", `  /invoices:
    get:
      operationId: listInvoices
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
        payer: { $ref: ../../schema/user.yaml#/components/schemas/User }
`),
		"services/identity/api.yaml": service("Identity", `  /users:
    get:
      operationId: listUsers
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: ../../schema/user.yaml#/components/schemas/User }
`),
	}
}

func TestUnifyMergesServices(t *testing.T) {
	text, doc, err := unify(t, twoServices())
	if err != nil {
		t.Fatal(err)
	}

	info := mapAt(t, doc, "info")
	if info["title"] != "Acme Platform API" || info["version"] != "2026.1" {
		t.Errorf("info = %v, want the configured title and version", info)
	}
	// A version like 2026.1 must stay a string, not be re-read as a float.
	if _, ok := info["version"].(string); !ok {
		t.Errorf("info.version = %T, want a string", info["version"])
	}

	paths := mapAt(t, doc, "paths")
	for _, want := range []string{"/invoices", "/users"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("path %q missing from the unified document", want)
		}
	}

	// Shared schemas are pooled once, not once per contributing service.
	schemas := mapAt(t, doc, "components", "schemas")
	for _, want := range []string{"Invoice", "User"} {
		if _, ok := schemas[want]; !ok {
			t.Errorf("component %q missing", want)
		}
	}
	if strings.Count(text, "\n        User:") != 1 {
		t.Errorf("User was emitted more than once:\n%s", text)
	}

	// Nothing may still point outside the document.
	if strings.Contains(text, "../../schema") {
		t.Errorf("unified document still references another file:\n%s", text)
	}

	servers := sliceAt(t, doc, "servers")
	if len(servers) != 1 {
		t.Fatalf("servers = %v, want exactly one", servers)
	}
	first := mapOf(t, servers[0], "servers[0]")
	if first["url"] != "https://api.acme.io" {
		t.Errorf("servers = %v, want the configured server", servers)
	}
}

// Tagging by Go package name would be useless, since services conventionally
// generate into a package called api; the service title is what distinguishes
// them.
func TestUnifyTagsByServiceTitle(t *testing.T) {
	_, doc, err := unify(t, twoServices())
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for i, tag := range sliceAt(t, doc, "tags") {
		entry := mapOf(t, tag, fmt.Sprintf("tags[%d]", i))
		name, ok := entry["name"].(string)
		if !ok {
			t.Fatalf("tags[%d].name is %T, want a string", i, entry["name"])
		}
		names = append(names, name)
	}
	if got := strings.Join(names, ","); got != "Billing,Identity" {
		t.Errorf("tags = %s, want Billing,Identity", got)
	}

	paths := mapAt(t, doc, "paths")
	billing := opTags(t, paths, "/invoices", "get")
	if len(billing) != 1 || billing[0] != "Billing" {
		t.Errorf("/invoices tags = %v, want [Billing]", billing)
	}
	identity := opTags(t, paths, "/users", "get")
	if len(identity) != 1 || identity[0] != "Identity" {
		t.Errorf("/users tags = %v, want [Identity]", identity)
	}
}

func TestUnifyPreservesAuthoredTags(t *testing.T) {
	files := twoServices()
	files["services/identity/api.yaml"] = service("Identity", `  /users:
    get:
      operationId: listUsers
      tags: [people]
      responses:
        "200": { description: ok }
`)
	_, doc, err := unify(t, files)
	if err != nil {
		t.Fatal(err)
	}
	got := opTags(t, mapAt(t, doc, "paths"), "/users", "get")
	if len(got) != 1 || got[0] != "people" {
		t.Errorf("tags = %v; an operation's own tags must be left alone", got)
	}
}

// Two services may own different methods of one path, which is a normal
// gateway arrangement.
func TestUnifyMergesMethodsOfOnePath(t *testing.T) {
	files := twoServices()
	files["services/billing/api.yaml"] = service("Billing", `  /things:
    get:
      operationId: getThing
      responses:
        "200": { description: ok }
`)
	files["services/identity/api.yaml"] = service("Identity", `  /things:
    post:
      operationId: postThing
      responses:
        "200": { description: ok }
`)
	_, doc, err := unify(t, files)
	if err != nil {
		t.Fatal(err)
	}
	thing := mapAt(t, doc, "paths", "/things")
	if _, ok := thing["get"]; !ok {
		t.Error("merged path lost the GET contributed by billing")
	}
	if _, ok := thing["post"]; !ok {
		t.Error("merged path lost the POST contributed by identity")
	}
}

func TestUnifyRejectsConflicts(t *testing.T) {
	tests := []struct {
		name              string
		billing, identity string
		want              string
	}{{
		name: "same method on same path",
		billing: service("Billing", `  /things:
    get:
      operationId: billingGetThing
      responses:
        "200": { description: ok }
`),
		identity: service("Identity", `  /things:
    get:
      operationId: identityGetThing
      responses:
        "200": { description: ok }
`),
		want: `path "/things" defines "get" in both`,
	}, {
		name: "duplicate operationId",
		billing: service("Billing", `  /a:
    get:
      operationId: dup
      responses:
        "200": { description: ok }
`),
		identity: service("Identity", `  /b:
    get:
      operationId: dup
      responses:
        "200": { description: ok }
`),
		want: `operationId "dup" is used by both`,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := twoServices()
			files["services/billing/api.yaml"] = tt.billing
			files["services/identity/api.yaml"] = tt.identity
			_, _, err := unify(t, files)
			if err == nil {
				t.Fatal("unify succeeded, want a conflict error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v\nwant it to contain %q", err, tt.want)
			}
		})
	}
}

func TestUnifyExcludesServices(t *testing.T) {
	files := twoServices()
	files["go-api-gen.yaml"] = unifiedConfig + "  exclude: [identity]\n"

	_, doc, err := unify(t, files)
	if err != nil {
		t.Fatal(err)
	}
	paths := mapAt(t, doc, "paths")
	if _, ok := paths["/users"]; ok {
		t.Error("an excluded service still contributed its paths")
	}
	if _, ok := paths["/invoices"]; !ok {
		t.Error("the included service lost its paths")
	}
}

func TestUnifyRejectsUnknownExclude(t *testing.T) {
	files := twoServices()
	files["go-api-gen.yaml"] = unifiedConfig + "  exclude: [nope]\n"

	_, _, err := unify(t, files)
	if err == nil {
		t.Fatal("config loaded, want an error for an exclude naming no " +
			"service")
	}
	if !strings.Contains(err.Error(), "does not name a service") {
		t.Errorf("error = %v, want guidance about the unmatched exclude", err)
	}
}

func TestUnifyIsDeterministic(t *testing.T) {
	files := twoServices()
	a, _, err := unify(t, files)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := unify(t, twoServices())
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("two unified documents from the same input differ")
	}
}

// securedUnifySource builds a spec with the given title, document-wide
// security, security schemes, and path entries.
func securedUnifySource(title, security, schemes, paths string) string {
	return service(title, paths) + security +
		"components:\n  securitySchemes:\n" + schemes
}

const bearerScheme = "    bearerAuth: { type: http, scheme: bearer }\n"

func TestUnifyHoistsSharedSecurity(t *testing.T) {
	files := twoServices()
	files["services/billing/api.yaml"] = securedUnifySource("Billing",
		"security:\n  - bearerAuth: []\n",
		bearerScheme, `  /invoices:
    get:
      operationId: listInvoices
      responses:
        "200": { description: ok }
`)
	// The same scheme, written differently and described in identity's own
	// words, is still the same scheme.
	files["services/identity/api.yaml"] = securedUnifySource("Identity",
		"security: [{ bearerAuth: [] }]\n",
		"    bearerAuth:\n      scheme: bearer\n      type: http\n"+
			"      description: A token identity accepts.\n", `  /users:
    get:
      operationId: listUsers
      responses:
        "200": { description: ok }
`)
	_, doc, err := unify(t, files)
	if err != nil {
		t.Fatal(err)
	}

	schemes := mapAt(t, doc, "components", "securitySchemes")
	if len(schemes) != 1 || mapAt(t, schemes, "bearerAuth")["type"] != "http" {
		t.Errorf("securitySchemes = %v, want bearerAuth once", schemes)
	}
	if got := sliceAt(t, doc, "security"); len(got) != 1 {
		t.Errorf("security = %v, want the shared requirement hoisted", got)
	}
	// Hoisted, so the operations go on inheriting it.
	for _, path := range []string{"/invoices", "/users"} {
		op := mapAt(t, doc, "paths", path, "get")
		if _, ok := op["security"]; ok {
			t.Errorf("%s has its own security, want it inherited", path)
		}
	}
}

// When services disagree, the top level can hold neither's security, so each
// operation that relied on its service's has to carry it itself.
func TestUnifyPushesDifferingSecurityOntoOperations(t *testing.T) {
	files := twoServices()
	files["services/billing/api.yaml"] = service("Billing", `  /invoices:
    get:
      operationId: listInvoices
      responses:
        "200": { description: ok }
`)
	files["services/identity/api.yaml"] = securedUnifySource("Identity",
		"security:\n  - bearerAuth: []\n",
		bearerScheme, `  /users:
    get:
      operationId: listUsers
      responses:
        "200": { description: ok }
  /login:
    post:
      operationId: login
      security: []
      responses:
        "200": { description: ok }
`)
	_, doc, err := unify(t, files)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := doc["security"]; ok {
		t.Errorf("security = %v, want none at the top level", doc["security"])
	}
	users := mapAt(t, doc, "paths", "/users", "get")
	if got := sliceAt(t, users, "security"); len(got) != 1 {
		t.Errorf("/users security = %v, want identity's bearerAuth", got)
	}
	login := mapAt(t, doc, "paths", "/login", "post")
	if got := sliceAt(t, login, "security"); len(got) != 0 {
		t.Errorf("/login security = %v, want its own opt-out kept", got)
	}
	invoices := mapAt(t, doc, "paths", "/invoices", "get")
	if _, ok := invoices["security"]; ok {
		t.Errorf("/invoices gained security %v, but billing declares none",
			invoices["security"])
	}
	schemes := mapAt(t, doc, "components", "securitySchemes")
	if _, ok := schemes["bearerAuth"]; !ok {
		t.Error("bearerAuth missing from the unified securitySchemes")
	}
}

func TestUnifyRejectsConflictingSecuritySchemes(t *testing.T) {
	files := twoServices()
	files["services/billing/api.yaml"] = securedUnifySource("Billing", "",
		bearerScheme, `  /invoices:
    get:
      operationId: listInvoices
      responses:
        "200": { description: ok }
`)
	files["services/identity/api.yaml"] = securedUnifySource("Identity", "",
		"    bearerAuth: { type: apiKey, in: header, name: X-Token }\n",
		`  /users:
    get:
      operationId: listUsers
      responses:
        "200": { description: ok }
`)
	_, _, err := unify(t, files)
	if err == nil {
		t.Fatal("unify succeeded, want a conflict error")
	}
	want := `securityScheme "bearerAuth" is declared differently`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v\nwant it to contain %q", err, want)
	}
}

// mapAt walks nested keys, checking each assertion, so a shape mismatch
// reports which key was wrong instead of panicking mid-chain.
func mapAt(t *testing.T, m map[string]any, keys ...string) map[string]any {
	t.Helper()
	cur, path := m, ""
	for _, k := range keys {
		path += "/" + k
		v, ok := cur[k]
		if !ok {
			t.Fatalf("key %s is missing", path)
		}
		next, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%s is %T, want a mapping", path, v)
		}
		cur = next
	}
	return cur
}

func mapOf(t *testing.T, v any, path string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want a mapping", path, v)
	}
	return m
}

func sliceAt(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q is missing", key)
	}
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("%q is %T, want a sequence", key, v)
	}
	return s
}

// opTags reads the tags of one operation, which is otherwise a chain of type
// assertions at every call site.
func opTags(t *testing.T, paths map[string]any, path, method string) []any {
	t.Helper()
	return sliceAt(t, mapAt(t, paths, path, method), "tags")
}

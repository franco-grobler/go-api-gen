package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/franco-grobler/go-api-gen/internal/config"
	"github.com/franco-grobler/go-api-gen/internal/spec"
)

// build writes a workspace to a temp dir and loads it.
func build(t *testing.T, files map[string]string) (*Workspace, string, error) {
	t.Helper()
	dir := t.TempDir()
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
		return nil, dir, err
	}
	w, err := Load(cfg)
	return w, dir, err
}

// A minimal set of files for a two-service monorepo sharing one schema root.
func monorepo() map[string]string {
	return map[string]string{
		"go.mod": "module github.com/acme/monorepo\n\ngo 1.24\n",
		"go-api-gen.yaml": `
version: 1
shared:
  apitypes:
    from: schema
    to: gen/apitypes
services:
  billing:
    spec: services/billing/api.yaml
    to: services/billing/internal/api
  identity:
    spec: services/identity/api.yaml
    to: services/identity/internal/api
`,
		"schema/user.yaml": `
components:
  schemas:
    User:
      type: object
      properties:
        id: { type: string }
        address: { $ref: '#/components/schemas/Address' }
    Address: { type: object, properties: { line1: { type: string } } }
    NeverUsed: { type: object }
`,
		"services/billing/api.yaml": `
openapi: 3.1.0
info: { title: Billing, version: "1" }
paths:
  /invoices:
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
      properties:
        payer: { $ref: ../../schema/user.yaml#/components/schemas/User }
`,
		"services/identity/api.yaml": `
openapi: 3.1.0
info: { title: Identity, version: "1" }
paths:
  /sessions:
    get:
      operationId: listSessions
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: ../../schema/user.yaml#/components/schemas/User }
`,
	}
}

func TestOwnershipAndImports(t *testing.T) {
	w, _, err := build(t, monorepo())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(w.Roots) != 1 || len(w.Services) != 2 {
		t.Fatalf(
			"got %d roots and %d services, want 1 and 2",
			len(w.Roots),
			len(w.Services),
		)
	}
	root, billing, identity := w.Roots[0], w.Services[0], w.Services[1]

	// Import paths are inferred from the enclosing go.mod.
	want := "github.com/acme/monorepo/gen/apitypes"
	if root.ImportPath != want {
		t.Errorf("root ImportPath = %q, want %q", root.ImportPath, want)
	}
	want = "github.com/acme/monorepo/services/billing/internal/api"
	if billing.ImportPath != want {
		t.Errorf("billing ImportPath = %q, want %q", billing.ImportPath, want)
	}

	// Invoice is billing's own; User and Address belong to the shared root.
	if len(billing.Local) != 1 || w.GoName(billing.Local[0]) != "Invoice" {
		t.Errorf(
			"billing.Local = %v, want just Invoice",
			names(w, billing.Local),
		)
	}
	if len(identity.Local) != 0 {
		t.Errorf(
			"identity.Local = %v, want nothing local",
			names(w, identity.Local),
		)
	}

	// The shared type is one Go type, imported by both services.
	user := find(t, w, root.Schemas, "User")
	if got := w.Qualify(user, billing); got != "apitypes.User" {
		t.Errorf("Qualify(User, billing) = %q, want apitypes.User", got)
	}
	if got := w.Qualify(user, identity); got != "apitypes.User" {
		t.Errorf("Qualify(User, identity) = %q, want apitypes.User", got)
	}
	invoice := billing.Local[0]
	if got := w.Qualify(invoice, billing); got != "Invoice" {
		t.Errorf(
			"Qualify(Invoice, billing) = %q, want an unqualified name",
			got,
		)
	}

	for _, p := range []*Service{billing, identity} {
		if len(p.Imports) != 1 || p.Imports[0] != root {
			t.Errorf(
				"%s Imports = %v, want the shared root",
				p.Name,
				p.Imports,
			)
		}
	}

	// The root generates everything it declares, including the schema no
	// service happens to reach; services tree-shake, roots do not.
	if len(root.Schemas) != 3 {
		t.Errorf("root has %d schemas, want all 3 declared", len(root.Schemas))
	}
	// But billing only reaches Invoice, User, Address, not NeverUsed.
	if got := strings.Join(
		names(w, billing.Reachable),
		",",
	); got != "Invoice,User,Address" {
		t.Errorf("billing.Reachable = %s, want Invoice,User,Address", got)
	}
}

func TestNameCollisionIsAnError(t *testing.T) {
	files := monorepo()
	files["schema/billing.yaml"] = "components:\n  schemas:\n    User: { " +
		"type: object }\n"

	_, _, err := build(t, files)
	if err == nil {
		t.Fatal("Load succeeded, want a collision error")
	}
	wants := []string{
		`Go type name "User" is claimed twice`, "x-go-name", "prefix",
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v\nwant it to contain %q", err, want)
		}
	}
}

func TestXGoNameResolvesCollision(t *testing.T) {
	files := monorepo()
	files["schema/billing.yaml"] = "components:\n  schemas:\n    User: { " +
		"type: object, x-go-name: BillingUser }\n"

	w, _, err := build(t, files)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var got []string
	for _, s := range w.Roots[0].Schemas {
		got = append(got, w.GoName(s))
	}
	if !contains(got, "BillingUser") || !contains(got, "User") {
		t.Errorf("names = %v, want both User and BillingUser", got)
	}
}

// A schema declared in one service's spec must not be reachable from another;
// generating it twice is the duplication the tool exists to avoid.
func TestCrossServiceLeakIsAnError(t *testing.T) {
	files := monorepo()
	files["services/identity/api.yaml"] = `
openapi: 3.1.0
info: { title: Identity, version: "1" }
paths:
  /sessions:
    get:
      operationId: listSessions
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: ../billing/api.yaml#/components/schemas/Invoice }
`
	_, _, err := build(t, files)
	if err == nil {
		t.Fatal("Load succeeded, want a cross-service reference error")
	}
	if !strings.Contains(err.Error(), "Move it into a shared root") {
		t.Errorf("error = %v\nwant guidance about moving the schema to a "+
			"shared root", err)
	}
}

func TestUncoveredFileIsAnError(t *testing.T) {
	files := monorepo()
	files["loose/thing.yaml"] = "components:\n  schemas:\n    Thing: { " +
		"type: object }\n"
	files["services/billing/api.yaml"] = `
openapi: 3.1.0
info: { title: Billing, version: "1" }
paths:
  /things:
    get:
      operationId: listThings
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: ../../loose/thing.yaml#/components/schemas/Thing }
`
	_, _, err := build(t, files)
	if err == nil {
		t.Fatal("Load succeeded, want an uncovered-file error")
	}
	if !strings.Contains(err.Error(), "not covered by any shared root") {
		t.Errorf("error = %v\nwant an uncovered-file error", err)
	}
}

func names(w *Workspace, ss []*spec.Schema) []string {
	var out []string
	for _, s := range ss {
		out = append(out, w.GoName(s))
	}
	return out
}

func find(
	t *testing.T,
	w *Workspace,
	ss []*spec.Schema,
	name string,
) *spec.Schema {
	t.Helper()
	for _, s := range ss {
		if w.GoName(s) == name {
			return s
		}
	}
	t.Fatalf("schema %q not found", name)
	return nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

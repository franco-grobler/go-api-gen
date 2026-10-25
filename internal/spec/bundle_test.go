package spec

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// bundleOf loads the billing fixture, tree-shakes it, and bundles it.
func bundleOf(
	t *testing.T,
	files map[string]string,
	specPath string,
) (text string, doc map[string]any) {
	t.Helper()
	dir := tree(t, files)
	l := NewLoader()
	d, err := l.Document(filepath.Join(dir, specPath))
	if err != nil {
		t.Fatalf("Document: %v", err)
	}

	var comps []BundleComponent
	for _, s := range Reachable(d) {
		comps = append(
			comps,
			BundleComponent{Schema: s, Name: s.Loc.ComponentName()},
		)
	}
	node, err := l.Bundle(d, BundleOptions{Components: comps})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	out, err := MarshalBundle(node)
	if err != nil {
		t.Fatalf("MarshalBundle: %v", err)
	}

	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("bundled output is not valid YAML: %v\n%s", err, out)
	}
	return string(out), doc
}

func TestBundleIsSelfContained(t *testing.T) {
	text, doc := bundleOf(t, map[string]string{
		"shared/user.yaml":          sharedUser,
		"services/billing/api.yaml": billingSpec,
	}, "services/billing/api.yaml")

	// No reference may still point outside the document.
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(
			line,
			"$ref",
		) && !strings.Contains(line, "'#/components/schemas/") &&
			!strings.Contains(
				line,
				`"#/components/schemas/`,
			) && !strings.Contains(line, "#/components/schemas/") {
			t.Errorf(
				"bundled output still has a non-local reference: %s",
				strings.TrimSpace(line),
			)
		}
	}
	if strings.Contains(text, "../../shared") {
		t.Errorf("bundled output still refers to another file:\n%s", text)
	}

	// The reachable shared schemas came along; the unreachable ones did not.
	schemas := mapAt(t, doc, "components", "schemas")
	for _, want := range []string{"Invoice", "User", "Address"} {
		if _, ok := schemas[want]; !ok {
			t.Errorf("component %q missing from the bundle", want)
		}
	}
	for _, unwanted := range []string{"Unused", "Orphan"} {
		if _, ok := schemas[unwanted]; ok {
			t.Errorf("unreachable component %q was bundled", unwanted)
		}
	}

	// A cross-file $ref to a bundled schema becomes a local reference.
	payer := mapAt(t, schemas, "Invoice", "properties", "payer")
	if got := payer["$ref"]; got != "#/components/schemas/User" {
		t.Errorf("payer $ref = %v, want #/components/schemas/User", got)
	}

	// A shared parameter is not a schema component, so it is inlined at its
	// use site rather than left dangling.
	invoices := mapAt(t, doc, "paths", "/invoices")
	params := sliceAt(t, invoices, "parameters")
	p := mapOf(t, params[0], "parameters[0]")
	if p["name"] != "page" || p["in"] != "query" {
		t.Errorf("shared parameter was not inlined: %#v", p)
	}
	if _, stillARef := p["$ref"]; stillARef {
		t.Errorf("shared parameter is still a reference: %#v", p)
	}
	schema := mapAt(t, p, "schema")
	if got := schema["minimum"]; got != 1 {
		t.Errorf("inlined parameter lost its constraints: %#v", schema)
	}
}

func TestBundleIsDeterministic(t *testing.T) {
	files := map[string]string{
		"shared/user.yaml":          sharedUser,
		"services/billing/api.yaml": billingSpec,
	}
	a, _ := bundleOf(t, files, "services/billing/api.yaml")
	b, _ := bundleOf(t, files, "services/billing/api.yaml")
	if a != b {
		t.Error("two bundles of the same input differ; output is not " +
			"deterministic")
	}
}

func TestBundleRenamesComponents(t *testing.T) {
	dir := tree(t, map[string]string{
		"shared/a.yaml": "components:\n  schemas:\n    User: { type: object, " +
			"properties: { id: { type: string } } }\n",
		"api.yaml": `
openapi: 3.1.0
paths:
  /x:
    get:
      operationId: x
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: shared/a.yaml#/components/schemas/User }
`,
	})
	l := NewLoader()
	d, err := l.Document(filepath.Join(dir, "api.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	reach := Reachable(d)
	// Bundle the shared schema under a disambiguated name, as the workspace
	// does when two roots define the same component name.
	node, err := l.Bundle(d, BundleOptions{
		Components: []BundleComponent{{Schema: reach[0], Name: "SharedUser"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalBundle(node)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "#/components/schemas/SharedUser") {
		t.Errorf("reference was not rewritten to the bundled name:\n%s", out)
	}
	if !strings.Contains(string(out), "SharedUser:") {
		t.Errorf("component was not emitted under the bundled name:\n%s", out)
	}
}

func TestBundlePreservesTopLevelOrderAndExtras(t *testing.T) {
	text, _ := bundleOf(t, map[string]string{
		"shared/user.yaml": sharedUser,
		"api.yaml": `
openapi: 3.1.0
info: { title: T, version: "1" }
servers:
  - { url: https://api.acme.io }
tags:
  - { name: billing }
paths:
  /x:
    get:
      operationId: x
      responses: { "200": { description: ok } }
`,
	}, "api.yaml")

	order := []string{"openapi:", "info:", "servers:", "tags:", "paths:"}
	last := -1
	for _, key := range order {
		i := strings.Index(text, "\n"+key)
		if i < 0 && !strings.HasPrefix(text, key) {
			t.Errorf("bundled output dropped top-level key %q:\n%s", key, text)
			continue
		}
		if i < last {
			t.Errorf("top-level key %q is out of source order:\n%s", key, text)
		}
		last = i
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
		cur = mapOf(t, v, path)
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

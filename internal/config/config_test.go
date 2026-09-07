package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspace writes a throwaway config file plus the paths it references and
// returns the config file path.
func workspace(t *testing.T, body string, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(
			p,
			[]byte("openapi: 3.1.0\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "go-api-gen.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const validBody = `
version: 1
shared:
  apitypes:
    from: schema
    to: gen/apitypes
types:
  format:uuid: { go: uuid.UUID, import: github.com/google/uuid }
errors:
  type_base_url: https://errors.acme.io/
services:
  billing:
    spec: services/billing/api.yaml
    to: services/billing/internal/api
`

func TestLoadDefaults(t *testing.T) {
	p := workspace(t, validBody, "schema/", "services/billing/api.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	root, svc := c.SharedRoots()[0], c.ServiceList()[0]

	// The key an entry is declared under is the name it is known by.
	if root.Name != "apitypes" || svc.Name != "billing" {
		t.Errorf("names = %q, %q; want apitypes, billing", root.Name, svc.Name)
	}
	// Package names are derived from the output directory basename, so a
	// service generating into internal/api is package api whatever it is
	// called.
	if root.Package != "apitypes" {
		t.Errorf("shared package = %q, want %q", root.Package, "apitypes")
	}
	if svc.Package != "api" {
		t.Errorf("service package = %q, want %q", svc.Package, "api")
	}
	// An omitted generate list means every target.
	if got := len(svc.Generate); got != len(AllTargets) {
		t.Errorf(
			"generate = %v, want all %d targets",
			svc.Generate,
			len(AllTargets),
		)
	}
	// Relative paths resolve against the config directory, not the cwd.
	wantTo := filepath.Join(filepath.Dir(p), "gen", "apitypes")
	if root.AbsTo != wantTo {
		t.Errorf("AbsTo = %q, want %q", root.AbsTo, wantTo)
	}
	if !filepath.IsAbs(svc.AbsSpec) {
		t.Errorf("AbsSpec = %q, want absolute", svc.AbsSpec)
	}
}

// Naming a target pulls in what it cannot be generated without, rather than
// failing on a combination that has only one possible meaning.
func TestTargetsImplyTheirPrerequisites(t *testing.T) {
	body := `
version: 1
services:
  a: { spec: a.yaml, to: outa, generate: [server] }
  b: { spec: b.yaml, to: outb, generate: [fake] }
`
	p := workspace(t, body, "a.yaml", "b.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{
		"a": "types, server",
		"b": "types, server, fake",
	}
	for _, s := range c.ServiceList() {
		got := joinTargets(s.Generate)
		if got != want[s.Name] {
			t.Errorf("%s generate = %q, want %q", s.Name, got, want[s.Name])
		}
	}
}

// A workspace-level generate list is the default, and a service overrides it
// outright rather than adding to it.
func TestWorkspaceGenerateIsTheDefault(t *testing.T) {
	body := `
version: 1
generate: [types, spec]
services:
  a: { spec: a.yaml, to: outa }
  b: { spec: b.yaml, to: outb, generate: [types] }
`
	p := workspace(t, body, "a.yaml", "b.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{"a": "types, spec", "b": "types"}
	for _, s := range c.ServiceList() {
		got := joinTargets(s.Generate)
		if got != want[s.Name] {
			t.Errorf("%s generate = %q, want %q", s.Name, got, want[s.Name])
		}
	}
}

func joinTargets(ts []Target) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = string(t)
	}
	return strings.Join(parts, ", ")
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		files []string
		want  string
	}{{
		name: "unsupported version",
		body: "version: 2\nservices:\n  a: { spec: a.yaml, to: out }\n",
		want: "unsupported version 2",
	}, {
		name: "missing spec file",
		body: "version: 1\nservices:\n  a: { spec: nope.yaml, to: out }\n",
		want: "spec \"nope.yaml\"",
	}, {
		// A diagnostic names the entry the author wrote, not its position.
		name: "diagnostics name the service",
		body: "version: 1\nservices:\n  billing: { spec: nope.yaml, to: o }\n",
		want: "services.billing:",
	}, {
		name: "unknown target",
		body: "version: 1\nservices:\n  a: { spec: a.yaml, to: out, " +
			"generate: [types, mocks] }\n",
		files: []string{"a.yaml"},
		want:  "services.a.generate: unknown target \"mocks\"",
	}, {
		name: "colliding outputs",
		body: "version: 1\nservices:\n  a: { spec: a.yaml, to: out }\n  " +
			"b: { spec: b.yaml, to: out }\n",
		files: []string{"a.yaml", "b.yaml"},
		want:  "is claimed by both",
	}, {
		name:  "shared root is a file",
		body:  "version: 1\nshared:\n  s: { from: a.yaml, to: out }\n",
		files: []string{"a.yaml"},
		want:  "is not a directory",
	}, {
		name: "empty config",
		body: "version: 1\n",
		want: "neither shared schema roots nor services",
	}, {
		name: "unknown field",
		body: "version: 1\nschemaz: []\n",
		want: "invalid keys: schemaz",
	}, {
		name: "qualified type without import",
		body: "version: 1\ntypes:\n  format:uuid: { go: uuid.UUID " +
			"}\nservices:\n  a: { spec: a.yaml, to: out }\n",
		files: []string{"a.yaml"},
		want: `go "uuid.UUID" is qualified by package "uuid" ` +
			`but no import is set`,
	}, {
		name: "mapping without a Go type",
		body: "version: 1\ntypes:\n  format:uuid: { import: " +
			"github.com/google/uuid }\nservices:\n  a: { spec: a.yaml, " +
			"to: out }\n",
		files: []string{"a.yaml"},
		want:  "types[\"format:uuid\"]: go is required",
	}, {
		name: "exclude names no service",
		body: "version: 1\nservices:\n  a: { spec: a.yaml, to: out }\n" +
			"unified:\n  to: gen/openapi.yaml\n  exclude: [nope]\n",
		files: []string{"a.yaml"},
		want:  `unified.exclude: "nope" does not name a service`,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := workspace(t, tt.body, tt.files...)
			_, err := Load(p)
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v\nwant it to contain %q", err, tt.want)
			}
		})
	}
}

func TestFindConfig(t *testing.T) {
	p := workspace(t, validBody, "schema/", "services/billing/api.yaml")
	deep := filepath.Join(filepath.Dir(p), "services", "billing")

	got, err := FindConfig(deep)
	if err != nil {
		t.Fatalf("FindConfig: %v", err)
	}
	if got != p {
		t.Errorf("FindConfig = %q, want %q", got, p)
	}

	if _, err := FindConfig(t.TempDir()); err == nil {
		t.Error("FindConfig in an empty tree succeeded, want error")
	}
}

func TestGoPackageName(t *testing.T) {
	tests := map[string]string{
		"/a/b/apitypes":   "apitypes",
		"/a/b/api-types":  "apitypes",
		"/a/b/API_Types":  "apitypes",
		"/a/b/2fa":        "api2fa",
		"/a/b/---":        "api",
		"/a/b/billing_v2": "billingv2",
	}
	for in, want := range tests {
		if got := goPackageName(in); got != want {
			t.Errorf("goPackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEnvOverride covers the reason for routing config through Viper: a value
// that differs per environment can be set without editing the file.
func TestEnvOverride(t *testing.T) {
	p := workspace(t, validBody, "schema/", "services/billing/api.yaml")

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "https://errors.acme.io/"; c.Errors.TypeBaseURL != want {
		t.Fatalf("TypeBaseURL = %q, want %q", c.Errors.TypeBaseURL, want)
	}

	t.Setenv("GOAPIGEN_ERRORS_TYPE_BASE_URL", "https://errors.staging.acme.io/")
	c, err = Load(p)
	if err != nil {
		t.Fatalf("Load with env override: %v", err)
	}
	if want := "https://errors.staging.acme.io/"; c.Errors.TypeBaseURL != want {
		t.Errorf(
			"TypeBaseURL = %q, want the environment override %q",
			c.Errors.TypeBaseURL,
			want,
		)
	}
}

// An unbound GOAPIGEN_ variable must not inject a key that strict decoding
// would then reject.
func TestUnboundEnvIsIgnored(t *testing.T) {
	t.Setenv("GOAPIGEN_SCHEMAZ", "oops")
	t.Setenv("GOAPIGEN_NONSENSE", "1")

	p := workspace(t, validBody, "schema/", "services/billing/api.yaml")
	if _, err := Load(p); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestJSONConfig(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"schema", "services/billing"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(dir, "services", "billing", "api.yaml"),
		[]byte("openapi: 3.1.0\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	body := `{
	  "version": 1,
	  "shared": {"apitypes": {"from": "schema", "to": "gen/apitypes"}},
	  "services": {
	    "billing": {
	      "spec": "services/billing/api.yaml",
	      "to": "services/billing/internal/api",
	      "generate": ["types", "spec"]
	    }
	  }
	}`
	p := filepath.Join(dir, "go-api-gen.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.SharedRoots()[0].Package; got != "apitypes" {
		t.Errorf("package = %q, want apitypes", got)
	}
	svc := c.ServiceList()[0]
	if !svc.Generates(TargetSpec) || svc.Generates(TargetServer) {
		t.Errorf(
			"generate = %v, want exactly types and spec",
			svc.Generate,
		)
	}

	// Discovery must find a non-YAML config too.
	found, err := FindConfig(filepath.Join(dir, "services", "billing"))
	if err != nil {
		t.Fatalf("FindConfig: %v", err)
	}
	if found != p {
		t.Errorf("FindConfig = %q, want %q", found, p)
	}
}

// Viper lower-cases every key it reads, so the accessors for the map-keyed
// sections have to be case-insensitive. Format mappings and schema mappings
// share the one "types" map, namespaced by a prefix a component name cannot
// contain.
func TestCaseInsensitiveLookups(t *testing.T) {
	body := `
version: 1
types:
  format:date-time: { go: time.Time, import: time }
  User: { go: apitypes.User, import: github.com/acme/monorepo/gen/apitypes }
services:
  a: { spec: a.yaml, to: out }
`
	p := workspace(t, body, "a.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if m, ok := c.FormatFor("date-time"); !ok || m.Go != "time.Time" {
		t.Errorf("FormatFor(date-time) = %v, %v", m, ok)
	}
	// A format mapping must not be reachable as a schema mapping, or a schema
	// called Uuid would pick up the uuid format's Go type.
	if m, ok := c.TypeFor("date-time"); ok {
		t.Errorf("TypeFor(date-time) = %v; format mappings are namespaced", m)
	}
	// The author wrote "User"; the lookup must find it whatever the casing.
	for _, name := range []string{"User", "user", "USER"} {
		m, ok := c.TypeFor(name)
		if !ok || m.Go != "apitypes.User" {
			t.Errorf(
				"TypeFor(%q) = %v, %v; want the mapping regardless of case",
				name,
				m,
				ok,
			)
		}
	}
}

// Viper lower-cases the names it reads as map keys but leaves the strings in
// unified.exclude alone, so the two are compared case-insensitively.
func TestExcludeMatchesNameCaseInsensitively(t *testing.T) {
	body := `
version: 1
services:
  Billing: { spec: a.yaml, to: out }
unified:
  to: gen/openapi.yaml
  exclude: [Billing]
`
	p := workspace(t, body, "a.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.Unified.Excludes(c.ServiceList()[0].Name) {
		t.Error("exclude did not match the service it names")
	}
}

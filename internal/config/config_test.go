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
schemas:
  - path: shared
    output: ../gen/apitypes
formats:
  uuid: { type: uuid.UUID, import: github.com/google/uuid }
errors:
  type_base_url: https://errors.acme.io/
packages:
  - spec: services/billing/api.yaml
    output: ../services/billing/internal/api
`

func TestLoadDefaults(t *testing.T) {
	p := workspace(t, validBody, "shared/", "services/billing/api.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Package names are derived from the output directory basename.
	if got := c.Schemas[0].Package; got != "apitypes" {
		t.Errorf("schema package = %q, want %q", got, "apitypes")
	}
	if got := c.Packages[0].Package; got != "api" {
		t.Errorf("package name = %q, want %q", got, "api")
	}
	// An omitted generate list means every target.
	if got := len(c.Packages[0].Generate); got != len(AllTargets) {
		t.Errorf(
			"generate = %v, want all %d targets",
			c.Packages[0].Generate,
			len(AllTargets),
		)
	}
	// Relative paths resolve against the config directory, not the cwd.
	wantOut := filepath.Join(filepath.Dir(filepath.Dir(p)), "gen", "apitypes")
	if c.Schemas[0].AbsOutput != wantOut {
		t.Errorf("AbsOutput = %q, want %q", c.Schemas[0].AbsOutput, wantOut)
	}
	if !filepath.IsAbs(c.Packages[0].AbsSpec) {
		t.Errorf("AbsSpec = %q, want absolute", c.Packages[0].AbsSpec)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		files []string
		want  string
	}{{
		name: "unsupported version",
		body: "version: 2\npackages:\n  - spec: a.yaml\n    output: out\n",
		want: "unsupported version 2",
	}, {
		name: "missing spec file",
		body: "version: 1\npackages:\n  - spec: nope.yaml\n    output: out\n",
		want: "spec \"nope.yaml\"",
	}, {
		name: "unknown target",
		body: "version: 1\npackages:\n  - spec: a.yaml\n    output: out\n    " +
			"generate: [types, mocks]\n",
		files: []string{"a.yaml"},
		want:  "unknown target \"mocks\"",
	}, {
		name: "validate without types",
		body: "version: 1\npackages:\n  - spec: a.yaml\n    output: out\n    " +
			"generate: [validate]\n",
		files: []string{"a.yaml"},
		want:  "\"validate\" requires \"types\"",
	}, {
		name: "fake without server",
		body: "version: 1\npackages:\n  - spec: a.yaml\n    output: out\n    " +
			"generate: [types, fake]\n",
		files: []string{"a.yaml"},
		want:  "\"fake\" requires \"server\"",
	}, {
		name: "colliding outputs",
		body: "version: 1\npackages:\n  - spec: a.yaml\n    output: out\n  - " +
			"spec: b.yaml\n    output: out\n",
		files: []string{"a.yaml", "b.yaml"},
		want:  "is claimed by both",
	}, {
		name:  "schema root is a file",
		body:  "version: 1\nschemas:\n  - path: a.yaml\n    output: out\n",
		files: []string{"a.yaml"},
		want:  "is not a directory",
	}, {
		name: "empty config",
		body: "version: 1\n",
		want: "neither schemas nor packages",
	}, {
		name: "unknown field",
		body: "version: 1\nschemaz: []\n",
		want: "invalid keys: schemaz",
	}, {
		name: "qualified type without import",
		body: "version: 1\nformats:\n  uuid: { type: uuid.UUID " +
			"}\npackages:\n  - spec: a.yaml\n    output: out\n",
		files: []string{"a.yaml"},
		want: `type "uuid.UUID" is qualified by package "uuid" ` +
			`but no import is set`,
	}, {
		name: "format without type",
		body: "version: 1\nformats:\n  uuid: { import: " +
			"github.com/google/uuid }\npackages:\n  - spec: a.yaml\n    " +
			"output: out\n",
		files: []string{"a.yaml"},
		want:  "formats[\"uuid\"]: type is required",
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
	p := workspace(t, validBody, "shared/", "services/billing/api.yaml")
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
	p := workspace(t, validBody, "shared/", "services/billing/api.yaml")

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

	p := workspace(t, validBody, "shared/", "services/billing/api.yaml")
	if _, err := Load(p); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestJSONConfig(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"shared", "services/billing"} {
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
	  "schemas": [{"path": "shared", "output": "../gen/apitypes"}],
	  "packages": [{
	    "spec": "services/billing/api.yaml",
	    "output": "../services/billing/internal/api",
	    "generate": ["types", "spec"]
	  }]
	}`
	p := filepath.Join(dir, "go-api-gen.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Schemas[0].Package != "apitypes" {
		t.Errorf("package = %q, want apitypes", c.Schemas[0].Package)
	}
	p0 := c.Packages[0]
	if !p0.Generates(TargetSpec) || p0.Generates(TargetServer) {
		t.Errorf(
			"generate = %v, want exactly types and spec",
			c.Packages[0].Generate,
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
// sections have to be case-insensitive.
func TestCaseInsensitiveLookups(t *testing.T) {
	body := `
version: 1
formats:
  date-time: { type: time.Time, import: time }
types:
  User: { type: apitypes.User, import: github.com/acme/monorepo/gen/apitypes }
packages:
  - spec: a.yaml
    output: out
`
	p := workspace(t, body, "a.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if m, ok := c.FormatFor("date-time"); !ok || m.Type != "time.Time" {
		t.Errorf("FormatFor(date-time) = %v, %v", m, ok)
	}
	// The author wrote "User"; the lookup must find it whatever the casing.
	for _, name := range []string{"User", "user", "USER"} {
		m, ok := c.TypeFor(name)
		if !ok || m.Type != "apitypes.User" {
			t.Errorf(
				"TypeFor(%q) = %v, %v; want the mapping regardless of case",
				name,
				m,
				ok,
			)
		}
	}
}

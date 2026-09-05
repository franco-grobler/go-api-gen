package gomod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSingleModule(t *testing.T) {
	root := t.TempDir()
	write(
		t,
		filepath.Join(root, "go.mod"),
		"module github.com/acme/monorepo\n\ngo 1.24\n",
	)

	r := NewResolver()
	tests := map[string]string{
		".":            "github.com/acme/monorepo",
		"gen/apitypes": "github.com/acme/monorepo/gen/apitypes",
		"services/billing/internal/api": "github.com/acme/monorepo" +
			"/services/billing/internal/api",
	}
	for rel, want := range tests {
		got, err := r.ImportPath(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if got != want {
			t.Errorf("ImportPath(%q) = %q, want %q", rel, got, want)
		}
	}
}

// TestMultiModule covers the go.work layout: each output directory must
// resolve against the module that actually encloses it, not the outermost one.
func TestMultiModule(t *testing.T) {
	root := t.TempDir()
	write(
		t,
		filepath.Join(root, "go.work"),
		"go 1.24\n\nuse (\n\t./shared\n\t./services/billing\n)\n",
	)
	write(
		t,
		filepath.Join(root, "shared", "go.mod"),
		"module github.com/acme/shared\n",
	)
	write(
		t,
		filepath.Join(root, "services", "billing", "go.mod"),
		"module github.com/acme/billing\n",
	)

	r := NewResolver()
	tests := map[string]string{
		"shared/apitypes":           "github.com/acme/shared/apitypes",
		"services/billing/internal": "github.com/acme/billing/internal",
	}
	for rel, want := range tests {
		got, err := r.ImportPath(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if got != want {
			t.Errorf("ImportPath(%q) = %q, want %q", rel, got, want)
		}
	}

	// A directory covered only by go.work, with no enclosing go.mod, is an
	// error.
	if _, err := r.ImportPath(filepath.Join(root, "docs")); err == nil {
		t.Error("expected an error for a directory with no enclosing go.mod")
	}
}

// The output directory does not exist when import paths are resolved.
func TestNonexistentDirectory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/m\n")

	got, err := NewResolver().ImportPath(filepath.Join(
		root,
		"does", "not", "exist", "yet",
	))
	if err != nil {
		t.Fatalf("ImportPath: %v", err)
	}
	if want := "example.com/m/does/not/exist/yet"; got != want {
		t.Errorf("ImportPath = %q, want %q", got, want)
	}
}

func TestModulePath(t *testing.T) {
	tests := map[string]string{
		"module example.com/m\n":            "example.com/m",
		"module \"example.com/m\"\n":        "example.com/m",
		"// c\nmodule example.com/m // x\n": "example.com/m",
		"go 1.24\nmodule example.com/m\n":   "example.com/m",
		"\tmodule   example.com/m  \n":      "example.com/m",
	}
	for in, want := range tests {
		got, err := modulePath([]byte(in))
		if err != nil {
			t.Fatalf("modulePath(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("modulePath(%q) = %q, want %q", in, got, want)
		}
	}

	// "modules" must not be mistaken for the module directive.
	if _, err := modulePath([]byte("modules example.com/m\n")); err == nil {
		t.Error("expected an error, got a match on a prefix")
	}
	_, err := modulePath([]byte("go 1.24\n"))
	if err == nil || !strings.Contains(err.Error(), "no module directive") {
		t.Errorf("err = %v, want a no-module-directive error", err)
	}
}

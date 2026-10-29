package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceFiles is a one-service workspace that configures a unified
// document, the smallest that exercises it.
var workspaceFiles = map[string]string{
	"go.mod": "module github.com/acme/monorepo\n\ngo 1.27.1\n",
	"go-api-gen.yaml": `
version: 1
services:
  billing:
    spec: services/billing/api.yaml
    to: services/billing/internal/api
    generate: [types, spec]
unified:
  to: docs/api/openapi.yaml
  title: Acme Platform API
  version: "1"
`,
	"services/billing/api.yaml": `openapi: 3.1.0
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
      required: [id]
      properties:
        id: { type: string }
`,
}

func writeFiles(t *testing.T, files map[string]string) string {
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
	return dir
}

// run executes the CLI with args, returning what it printed.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestBuildWritesUnifiedAndCheckHoldsIt(t *testing.T) {
	dir := writeFiles(t, workspaceFiles)
	cfg := filepath.Join(dir, "go-api-gen.yaml")
	unified := filepath.Join(dir, "docs", "api", "openapi.yaml")

	out, _, err := run(t, "build", "-c", cfg)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if !strings.Contains(out, filepath.Join("docs", "api", "openapi.yaml")) {
		t.Errorf("build did not list the unified spec:\n%s", out)
	}
	built, err := os.ReadFile(unified)
	if err != nil {
		t.Fatalf("build did not write the unified spec: %v", err)
	}
	if !strings.Contains(string(built), "/invoices:") {
		t.Errorf("unified spec lacks the billing service:\n%s", built)
	}

	// unify writes the same bytes, so running it after build is a no-op.
	if out, _, err := run(t, "unify", "-c", cfg); err != nil {
		t.Fatalf("unify: %v\n%s", err, out)
	}
	unifiedAgain, err := os.ReadFile(unified)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(built, unifiedAgain) {
		t.Errorf("unify wrote\n%s\nbuild wrote\n%s", unifiedAgain, built)
	}

	if out, errOut, err := run(t, "check", "-c", cfg); err != nil {
		t.Fatalf("check after build: %v\n%s%s", err, out, errOut)
	}

	// A stale unified document fails check, and is named, even though
	// every per-service file is current.
	stale := strings.Replace(string(built), "/invoices:", "/bills:", 1)
	if err := os.WriteFile(unified, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	assertStale(t, cfg)

	// And so does a missing one.
	if err := os.Remove(unified); err != nil {
		t.Fatal(err)
	}
	assertStale(t, cfg)
}

func assertStale(t *testing.T, cfg string) {
	t.Helper()
	_, errOut, err := run(t, "check", "-c", cfg)
	var silent silentError
	if !errors.As(err, &silent) {
		t.Fatalf("check = %v, want it to report the output out of date", err)
	}
	want := filepath.Join("docs", "api", "openapi.yaml")
	if !strings.Contains(errOut, "1 generated file(s) are out of date") ||
		!strings.Contains(errOut, want) {
		t.Errorf("check reported\n%s\nwant only %s out of date", errOut, want)
	}
}

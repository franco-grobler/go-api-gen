package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes name/content pairs into a temp dir and returns the dir.
func tree(t *testing.T, files map[string]string) string {
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

// TestSharedSchemaIdentity is the premise of the whole tool: two service specs
// that reference the same shared component must resolve to one schema, not two
// structurally equal copies.
func TestSharedSchemaIdentity(t *testing.T) {
	dir := tree(t, map[string]string{
		"shared/user.yaml": `
components:
  schemas:
    User:
      type: object
      required: [id]
      properties:
        id: { type: string, format: uuid }
`,
		"services/billing/api.yaml": `
components:
  schemas:
    Invoice:
      type: object
      properties:
        payer: { $ref: ../../shared/user.yaml#/components/schemas/User }
`,
		"services/identity/api.yaml": `
components:
  schemas:
    Session:
      type: object
      properties:
        subject: { $ref: ../../shared/user.yaml#/components/schemas/User }
`,
	})

	l := NewLoader()
	invoice, err := l.Schema(Location{filepath.Join(
		dir,
		"services", "billing", "api.yaml",
	), "/components/schemas/Invoice"})
	if err != nil {
		t.Fatalf("billing: %v", err)
	}
	session, err := l.Schema(Location{filepath.Join(
		dir,
		"services", "identity", "api.yaml",
	), "/components/schemas/Session"})
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	payer := invoice.Prop("payer").Schema
	subject := session.Prop("subject").Schema
	if payer != subject {
		t.Fatal("the same shared component resolved to two distinct schemas; " +
			"shared types would be duplicated per service")
	}
	if !payer.Named() {
		t.Errorf(
			"shared schema is not recognised as a named component: %v",
			payer.Loc,
		)
	}
	if got := payer.Loc.ComponentName(); got != "User" {
		t.Errorf("ComponentName = %q, want %q", got, "User")
	}
	if !filepath.IsAbs(payer.Loc.File) || !strings.HasSuffix(
		payer.Loc.File,
		"shared/user.yaml",
	) {
		t.Errorf("Loc.File = %q, want the defining shared file", payer.Loc.File)
	}
}

// TestRecursiveSchema covers the memo-before-fill ordering; without it this
// input recurses forever.
func TestRecursiveSchema(t *testing.T) {
	dir := tree(t, map[string]string{"a.yaml": `
components:
  schemas:
    Node:
      type: object
      properties:
        name: { type: string }
        parent: { $ref: '#/components/schemas/Node' }
        children:
          type: array
          items: { $ref: '#/components/schemas/Node' }
`})
	l := NewLoader()
	node, err := l.Schema(Location{filepath.Join(
		dir,
		"a.yaml",
	), "/components/schemas/Node"})
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if node.Prop("parent").Schema != node {
		t.Error("self-reference did not resolve back to the same schema")
	}
	if node.Prop("children").Schema.Items != node {
		t.Error("self-reference through an array did not resolve back to the " +
			"same schema")
	}

	// Walk must terminate on a cyclic schema.
	count := 0
	node.Walk(func(*Schema) bool { count++; return true })
	if count == 0 {
		t.Error("Walk visited nothing")
	}
}

func TestAliasComponentResolvesThrough(t *testing.T) {
	dir := tree(t, map[string]string{"a.yaml": `
components:
  schemas:
    User: { type: object, properties: { id: { type: string } } }
    Owner: { $ref: '#/components/schemas/User' }
`})
	l := NewLoader()
	user, err := l.Schema(Location{filepath.Join(
		dir,
		"a.yaml",
	), "/components/schemas/User"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := l.Schema(Location{filepath.Join(
		dir,
		"a.yaml",
	), "/components/schemas/Owner"})
	if err != nil {
		t.Fatal(err)
	}
	if owner != user {
		t.Error("an alias component should resolve through to its target " +
			"rather than mint a second type")
	}
}

func TestKeywordParsing(t *testing.T) {
	dir := tree(t, map[string]string{"a.yaml": `
components:
  schemas:
    S:
      type: [string, "null"]
      minLength: 3
      maxLength: 10
      pattern: '^[a-z]+$'
      x-validate: [reserved_words]
    N:
      type: integer
      minimum: 0
      exclusiveMaximum: 100
    Legacy:
      type: integer
      minimum: 0
      exclusiveMinimum: true
    E:
      type: string
      enum: [red, green]
`})
	l := NewLoader()
	load := func(name string) *Schema {
		t.Helper()
		s, err := l.Schema(Location{filepath.Join(
			dir,
			"a.yaml",
		), "/components/schemas/" + name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return s
	}

	s := load("S")
	if !s.Nullable {
		t.Error(`type: [string, "null"] should set Nullable`)
	}
	if got := s.SingleType(); got != "string" {
		t.Errorf(`SingleType = %q, want "string" with null stripped`, got)
	}
	if s.MinLength == nil || *s.MinLength != 3 {
		t.Errorf("MinLength = %v, want 3", s.MinLength)
	}
	if len(s.Validate) != 1 || s.Validate[0] != "reserved_words" {
		t.Errorf("Validate = %v, want [reserved_words]", s.Validate)
	}

	n := load("N")
	if n.ExclusiveMaximum == nil || *n.ExclusiveMaximum != 100 {
		t.Errorf("ExclusiveMaximum = %v, want 100", n.ExclusiveMaximum)
	}

	// The OpenAPI 3.0 boolean spelling converts the inclusive bound in place.
	legacy := load("Legacy")
	if legacy.Minimum != nil {
		t.Errorf(
			"Minimum = %v, want nil after conversion to an exclusive bound",
			legacy.Minimum,
		)
	}
	if legacy.ExclusiveMinimum == nil || *legacy.ExclusiveMinimum != 0 {
		t.Errorf("ExclusiveMinimum = %v, want 0", legacy.ExclusiveMinimum)
	}

	if e := load("E"); !e.IsEnum() || len(e.Enum) != 2 {
		t.Errorf("Enum = %v, want two values", e.Enum)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{{
		name: "ref with sibling constraints",
		body: "components:\n  schemas:\n    A: { $ref: " +
			"'#/components/schemas/B', minLength: 3 }\n    B: { type: string " +
			"}\n",
		want: "$ref cannot be combined with \"minLength\"",
	}, {
		name: "unknown keyword",
		body: "components:\n  schemas:\n" +
			"    A: { type: string, minLenght: 3 }\n",
		want: "unknown schema keyword(s) \"minLenght\"",
	}, {
		name: "unsupported keyword",
		body: "components:\n  schemas:\n    A: { type: object, " +
			"patternProperties: {} }\n",
		want: "patternProperties is not supported",
	}, {
		name: "alias cycle",
		body: "components:\n  schemas:\n    A: { $ref: " +
			"'#/components/schemas/B' }\n    B: { $ref: " +
			"'#/components/schemas/A' }\n",
		want: "$ref cycle",
	}, {
		name: "dangling pointer",
		body: "components:\n  schemas:\n    A: { $ref: " +
			"'#/components/schemas/Nope' }\n",
		want: "key \"Nope\" not found",
	}, {
		name: "remote ref",
		body: "components:\n  schemas:\n    A: { $ref: " +
			"'https://example.com/s.yaml#/X' }\n",
		want: "remote references are not supported",
	}, {
		name: "missing file",
		body: "components:\n  schemas:\n    A: { $ref: " +
			"'./nope.yaml#/components/schemas/X' }\n",
		want: "read spec",
	}, {
		name: "bad type for constraint",
		body: "components:\n  schemas:\n    A: { type: string, minLength: " +
			"many }\n",
		want: "expected a non-negative integer",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tree(t, map[string]string{"a.yaml": tt.body})
			l := NewLoader()
			_, err := l.Schema(Location{filepath.Join(
				dir,
				"a.yaml",
			), "/components/schemas/A"})
			if err == nil {
				t.Fatal("Schema succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v\nwant it to contain %q", err, tt.want)
			}
		})
	}
}

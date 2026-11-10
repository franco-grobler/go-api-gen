package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// users is an OpenAPI 3.0 service holding nullable schemas of every shape,
// in each place a schema can appear.
const users30 = `openapi: 3.0.3
info: { title: Users, version: "1" }
paths:
  /users/{id}:
    parameters:
      - { name: id, in: path, required: true, schema: { type: string } }
    patch:
      operationId: updateUser
      parameters:
        - name: cursor
          in: query
          schema: { type: string, nullable: true }
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: ./schemas/UserUpdate.yaml }
      responses:
        "200":
          description: ok
          headers:
            X-Next:
              schema: { type: string, nullable: true }
          content:
            application/json:
              schema: { $ref: '#/components/schemas/User' }
components:
  schemas:
    User:
      type: object
      required: [id]
      properties:
        id: { type: string }
        country: { type: string, nullable: true, minLength: 2 }
        bio: { type: string, nullable: false }
        status: { type: string, enum: [active, banned], nullable: true }
        manager:
          description: Who manages the user.
          allOf: [{ $ref: '#/components/schemas/Ref' }]
          nullable: true
        team:
          allOf: [{ $ref: '#/components/schemas/Ref' }, { nullable: true }]
        pet:
          oneOf:
            - { $ref: '#/components/schemas/Cat' }
            - { $ref: '#/components/schemas/Dog' }
          nullable: true
        aliases:
          type: array
          nullable: true
          items: { type: string, nullable: true }
        labels:
          type: object
          additionalProperties: { type: integer, nullable: true }
        nickname: { $ref: '#/components/schemas/Nickname' }
        nullable:
          type: boolean
          example: { nullable: true }
    Nickname: { type: string, nullable: true }
    Ref: { type: object, properties: { id: { type: string } } }
    Cat: { type: object, properties: { meow: { type: boolean } } }
    Dog: { type: object, properties: { bark: { type: boolean } } }
`

// billing is OpenAPI 3.1. It reaches a fragment that declares no version,
// which is then 3.1 too, and a file that declares 3.0 of its own.
const billing31 = `openapi: 3.1.0
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
        note: { type: [string, "null"] }
        legacy: { type: string, nullable: true }
        memo: { $ref: ./schemas/Memo.yaml }
        total: { $ref: ./money.yaml#/components/schemas/Money }
`

func nullableWorkspace(t *testing.T) string {
	t.Helper()
	return tree(t, map[string]string{
		"users.yaml":   users30,
		"billing.yaml": billing31,
		"schemas/UserUpdate.yaml": `type: object
minProperties: 1
properties:
  country: { type: string, nullable: true }
  address: { $ref: ./Address.yaml }
`,
		"schemas/Address.yaml": `type: object
properties:
  line2: { type: string, nullable: true }
`,
		"schemas/Memo.yaml": "{ type: string, nullable: true }\n",
		"money.yaml": `openapi: 3.0.0
components:
  schemas:
    Money:
      type: object
      properties:
        currency: { type: string, nullable: true }
`,
	})
}

// unifyFiles unifies the named documents of dir, as gen does: every reachable
// schema becomes a component under its own name.
func unifyFiles(
	t *testing.T,
	dir, openapi string,
	names ...string,
) (l *Loader, docs []*Document, out []byte) {
	t.Helper()
	l = NewLoader()
	opts := &UnifyOptions{
		OpenAPI: openapi,
		Info:    UnifyInfo{Title: "Platform", Version: "1"},
	}
	seen := map[*Schema]bool{}
	for _, name := range names {
		d, err := l.Document(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("Document(%s): %v", name, err)
		}
		docs = append(docs, d)
		opts.Sources = append(opts.Sources, UnifySource{Doc: d})
		for _, s := range Reachable(d) {
			if !seen[s] {
				seen[s] = true
				opts.Components = append(opts.Components, BundleComponent{
					Schema: s, Name: s.Loc.ComponentName(),
				})
			}
		}
	}
	n, err := l.Unify(opts)
	if err != nil {
		t.Fatalf("Unify: %v", err)
	}
	if out, err = MarshalBundle(n); err != nil {
		t.Fatal(err)
	}
	return l, docs, out
}

func TestUnifyTranslatesNullableFrom30(t *testing.T) {
	dir := nullableWorkspace(t)
	_, docs, out := unifyFiles(t, dir, "", "users.yaml", "billing.yaml")
	text := string(out)
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unified output is not YAML: %v\n%s", err, out)
	}
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v, want 3.1.0", doc["openapi"])
	}

	null := map[string]any{"type": "null"}
	ref := func(name string) map[string]any {
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	nullable := func(t string) []any { return []any{t, "null"} }

	schemas := mapAt(t, doc, "components", "schemas")
	user := mapAt(t, schemas, "User", "properties")
	op := mapAt(t, doc, "paths", "/users/{id}", "patch")
	body := mapAt(t, op, "requestBody", "content", "application/json",
		"schema")
	invoice := mapAt(t, schemas, "Invoice", "properties")
	tests := []struct {
		at   string
		got  any
		want any
	}{
		{"country", user["country"], map[string]any{
			"type": nullable("string"), "minLength": 2,
		}},
		{"bio", user["bio"], map[string]any{"type": "string"}},
		{"status", user["status"], map[string]any{
			"type": nullable("string"), "enum": []any{"active", "banned", nil},
		}},
		{"manager", user["manager"], map[string]any{
			"description": "Who manages the user.",
			"anyOf":       []any{ref("Ref"), null},
		}},
		{"team", user["team"], map[string]any{
			"anyOf": []any{ref("Ref"), null},
		}},
		{"pet", user["pet"], map[string]any{
			"oneOf": []any{ref("Cat"), ref("Dog"), null},
		}},
		{"aliases", user["aliases"], map[string]any{
			"type": nullable("array"), "items": map[string]any{
				"type": nullable("string"),
			},
		}},
		{"labels", user["labels"], map[string]any{
			"type": "object", "additionalProperties": map[string]any{
				"type": nullable("integer"),
			},
		}},
		// A property named nullable, and an example holding the key, are
		// not the keyword.
		{"property named nullable", user["nullable"], map[string]any{
			"type": "boolean", "example": map[string]any{"nullable": true},
		}},
		{"named component", schemas["Nickname"], map[string]any{
			"type": nullable("string"),
		}},
		{"query parameter", mapOf(t, sliceAt(t, op, "parameters")[0],
			"parameters[0]")["schema"], map[string]any{
			"type": nullable("string"),
		}},
		{"response header", mapAt(t, op, "responses", "200", "headers",
			"X-Next")["schema"], map[string]any{"type": nullable("string")}},
		// A fragment declaring no version is in the 3.0 document's, through
		// a fragment of its own.
		{"request body", mapAt(t, body, "properties")["country"],
			map[string]any{"type": nullable("string")}},
		{"nested fragment", mapAt(t, body, "properties", "address",
			"properties")["line2"], map[string]any{
			"type": nullable("string"),
		}},
		// The 3.1 document is left as written, and so is a fragment only it
		// reaches; a file declaring 3.0 is translated whoever reaches it.
		{"3.1 null type", invoice["note"], map[string]any{
			"type": nullable("string"),
		}},
		{"3.1 source", invoice["legacy"], map[string]any{
			"type": "string", "nullable": true,
		}},
		{"3.1 fragment", invoice["memo"], map[string]any{
			"type": "string", "nullable": true,
		}},
		{"3.0 file reached from 3.1", mapAt(t, schemas, "Money",
			"properties")["currency"], map[string]any{
			"type": nullable("string"),
		}},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s = %v, want %v", tt.at, tt.got, tt.want)
		}
	}
	if !strings.Contains(text, `country: {type: [string, "null"], `) &&
		!strings.Contains(text, `type: [string, "null"]`) {
		t.Errorf("want type lists written as [T, \"null\"]:\n%s", text)
	}

	// Loaded back, the unified document is as nullable as its sources.
	path := filepath.Join(dir, "unified.yaml")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := NewLoader().Document(path)
	if err != nil {
		t.Fatalf("loading the unified document back: %v\n%s", err, out)
	}
	c := &nullabilityCheck{t: t, seen: map[[2]*Schema]bool{}}
	for _, d := range docs {
		c.document(d, back)
	}
}

// A unified document that stays 3.0 keeps nullable, which means something
// there.
func TestUnifyTo30KeepsNullable(t *testing.T) {
	dir := nullableWorkspace(t)
	_, _, out := unifyFiles(t, dir, "3.0.3", "users.yaml")
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	country := mapAt(t, doc, "components", "schemas", "User", "properties",
		"country")
	if country["nullable"] != true || country["type"] != "string" {
		t.Errorf("country = %v, want it as written", country)
	}
}

// nullabilityCheck compares the nullability of every schema of a source
// document with its copy in the unified document.
type nullabilityCheck struct {
	t    *testing.T
	seen map[[2]*Schema]bool
}

func (c *nullabilityCheck) document(src, unified *Document) {
	c.t.Helper()
	for _, s := range src.Components {
		got := componentNamed(unified, s.Loc.ComponentName())
		if got == nil {
			c.t.Errorf("component %s is missing", s.Loc.ComponentName())
			continue
		}
		c.schema(s.Loc.ComponentName(), s, got)
	}
	ops := map[string]*Operation{}
	for _, pi := range unified.Paths {
		for _, op := range pi.Operations {
			ops[op.OperationID] = op
		}
	}
	for _, pi := range src.Paths {
		for _, op := range pi.Operations {
			got := ops[op.OperationID]
			if got == nil {
				c.t.Errorf("operation %s is missing", op.OperationID)
				continue
			}
			c.operation(op, got)
		}
	}
}

func (c *nullabilityCheck) operation(src, got *Operation) {
	c.t.Helper()
	at := src.OperationID
	for i, p := range src.Parameters {
		c.schema(fmt.Sprintf("%s parameter %s", at, p.Name), p.Schema,
			got.Parameters[i].Schema)
	}
	if src.RequestBody != nil {
		c.schema(at+" body", src.RequestBody.JSON().Schema,
			got.RequestBody.JSON().Schema)
	}
	for i, r := range src.Responses {
		g := got.Responses[i]
		for j, h := range r.Headers {
			c.schema(fmt.Sprintf("%s %s header %s", at, r.Status, h.Name),
				h.Schema, g.Headers[j].Schema)
		}
		if m := r.JSON(); m != nil {
			c.schema(at+" "+r.Status, m.Schema, g.JSON().Schema)
		}
	}
}

func (c *nullabilityCheck) schema(at string, src, got *Schema) {
	c.t.Helper()
	if src == nil || got == nil {
		if src != got {
			c.t.Errorf("%s: got %v, want %v", at, got, src)
		}
		return
	}
	if c.seen[[2]*Schema{src, got}] {
		return
	}
	c.seen[[2]*Schema{src, got}] = true

	if src.Nullable != got.Nullable {
		c.t.Errorf("%s: nullable is %v after unify, was %v",
			at, got.Nullable, src.Nullable)
	}
	src, got = wrapped(src), wrapped(got)
	if src.Named() != got.Named() ||
		src.Loc.ComponentName() != got.Loc.ComponentName() {
		c.t.Errorf("%s: is %s after unify, was %s", at, got.Loc, src.Loc)
		return
	}
	if !reflect.DeepEqual(src.Type, got.Type) ||
		!reflect.DeepEqual(src.Enum, got.Enum) {
		c.t.Errorf("%s: type %v enum %v after unify, was %v %v",
			at, got.Type, got.Enum, src.Type, src.Enum)
	}
	for _, p := range src.Properties {
		gp := got.Prop(p.Name)
		if gp == nil {
			c.t.Errorf("%s: property %s is missing", at, p.Name)
			continue
		}
		c.schema(at+"."+p.Name, p.Schema, gp.Schema)
	}
	c.schema(at+"[]", src.Items, got.Items)
	c.schema(at+"{}", src.AdditionalPro, got.AdditionalPro)
	lists := [][2][]*Schema{
		{src.AllOf, got.AllOf}, {src.OneOf, got.OneOf}, {src.AnyOf, got.AnyOf},
	}
	for _, l := range lists {
		if len(l[0]) != len(l[1]) {
			c.t.Errorf("%s: %d members after unify, was %d",
				at, len(l[1]), len(l[0]))
			continue
		}
		for i := range l[0] {
			c.schema(fmt.Sprintf("%s|%d", at, i), l[0][i], l[1][i])
		}
	}
}

// wrapped follows annotation-only wrappers to the schema they stand for, as
// the generator does: allOf [X] and anyOf [X] alike mean X.
func wrapped(s *Schema) *Schema {
	for w := s.Wrapped(); w != nil; w = s.Wrapped() {
		s = w
	}
	return s
}

func componentNamed(d *Document, name string) *Schema {
	for _, s := range d.Components {
		if s.Loc.ComponentName() == name {
			return s
		}
	}
	return nil
}

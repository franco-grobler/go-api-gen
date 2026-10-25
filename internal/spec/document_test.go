package spec

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

const sharedUser = `
components:
  schemas:
    User:
      type: object
      required: [id, email]
      properties:
        id: { type: string, format: uuid }
        email: { type: string, format: email }
        address: { $ref: '#/components/schemas/Address' }
    Address:
      type: object
      properties:
        line1: { type: string }
    Unused:
      type: object
      properties:
        nope: { type: string }
  parameters:
    Page:
      name: page
      in: query
      schema: { type: integer, minimum: 1 }
`

const billingSpec = `
openapi: 3.1.0
info: { title: Billing, version: "1.0" }
paths:
  /invoices:
    parameters:
      - $ref: ../../shared/user.yaml#/components/parameters/Page
    get:
      operationId: listInvoices
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: array
                items: { $ref: '#/components/schemas/Invoice' }
  /invoices/{invoiceId}:
    get:
      operationId: getInvoice
      parameters:
        - name: invoiceId
          in: path
          required: true
          schema: { type: string }
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Invoice' }
        "404": { description: gone }
components:
  schemas:
    Invoice:
      type: object
      properties:
        payer: { $ref: ../../shared/user.yaml#/components/schemas/User }
    Orphan:
      type: object
`

func billingDoc(t *testing.T) (doc *Document, dir string) {
	t.Helper()
	dir = tree(t, map[string]string{
		"shared/user.yaml":          sharedUser,
		"services/billing/api.yaml": billingSpec,
	})
	l := NewLoader()
	doc, err := l.Document(
		filepath.Join(dir, "services", "billing", "api.yaml"))
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	return doc, dir
}

func TestDocumentParse(t *testing.T) {
	d, _ := billingDoc(t)

	if d.Info.Title != "Billing" {
		t.Errorf("Info.Title = %q", d.Info.Title)
	}
	if len(d.Paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(d.Paths))
	}

	list := d.Paths[0].Operations[0]
	if list.OperationID != "listInvoices" || list.Method != http.MethodGet {
		t.Errorf("op = %s %s", list.Method, list.OperationID)
	}
	// The path-level parameter, itself a cross-file $ref, must be merged in.
	q := list.Params("query")
	if len(q) != 1 || q[0].Name != "page" {
		t.Fatalf("query params = %v, want the shared page parameter", q)
	}
	if q[0].Loc.IsZero() {
		t.Error("a $ref'd parameter should record where it was defined")
	}

	get := d.Paths[1].Operations[0]
	if got := len(get.Responses); got != 2 {
		t.Fatalf("got %d responses, want 2", got)
	}
	if get.Responses[0].Status != "200" || get.Responses[1].Status != "404" {
		t.Errorf(
			"responses out of order: %s, %s",
			get.Responses[0].Status,
			get.Responses[1].Status,
		)
	}
	if get.JSONBodySchemaName() == "" {
		t.Error("expected the 200 response to resolve to the Invoice schema")
	}
}

// JSONBodySchemaName is a test helper reaching the 200 response schema name.
func (o *Operation) JSONBodySchemaName() string {
	for _, r := range o.Success() {
		if m := r.JSON(); m != nil && m.Schema.Named() {
			return m.Schema.Loc.ComponentName()
		}
	}
	return ""
}

func TestReachableTreeShakes(t *testing.T) {
	d, _ := billingDoc(t)
	var names []string
	for _, s := range Reachable(d) {
		names = append(names, s.Loc.ComponentName())
	}
	got := strings.Join(names, ",")

	// Invoice pulls in the shared User, which pulls in Address. Unused (shared)
	// and Orphan (local) are declared but unreachable, and must not appear.
	want := "Invoice,User,Address"
	if got != want {
		t.Errorf("Reachable = %s, want %s", got, want)
	}
}

func TestPathParamValidation(t *testing.T) {
	tests := []struct{ name, spec, want string }{{
		name: "undeclared path variable",
		spec: `
paths:
  /a/{id}:
    get:
      operationId: a
      responses: { "200": { description: ok } }
`,
		want: `path template "/a/{id}" uses "id"`,
	}, {
		name: "declared but not in template",
		spec: `
paths:
  /a:
    get:
      operationId: a
      parameters:
        - name: id
          in: path
          required: true
          schema: { type: string }
      responses: { "200": { description: ok } }
`,
		want: `does not appear in the path template`,
	}, {
		name: "optional path parameter",
		spec: `
paths:
  /a/{id}:
    get:
      operationId: a
      parameters: [{ name: id, in: path, schema: { type: string } }]
      responses: { "200": { description: ok } }
`,
		want: `path parameter "id" must be required`,
	}, {
		name: "duplicate operation id",
		spec: `
paths:
  /a:
    get:
      operationId: dup
      responses: { "200": { description: ok } }
  /b:
    get:
      operationId: dup
      responses: { "200": { description: ok } }
`,
		want: `operationId "dup" is already used`,
	}, {
		name: "no responses",
		spec: `
paths:
  /a:
    get:
      operationId: a
`,
		want: "declares no responses",
	}, {
		name: "bad status key",
		spec: `
paths:
  /a:
    get:
      operationId: a
      responses: { okay: { description: ok } }
`,
		want: `is not a status code`,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tree(t, map[string]string{"a.yaml": tt.spec})
			_, err := NewLoader().Document(filepath.Join(dir, "a.yaml"))
			if err == nil {
				t.Fatal("Document succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v\nwant it to contain %q", err, tt.want)
			}
		})
	}
}

func TestDeriveOperationID(t *testing.T) {
	tests := map[string]string{
		"GET /users":             "getUsers",
		"GET /users/{id}":        "getUsersId",
		"POST /users/{id}/roles": "postUsersIdRoles",
		"DELETE /v1/api-keys":    "deleteV1ApiKeys",
		"GET /":                  "get",
	}
	for in, want := range tests {
		method, path, _ := strings.Cut(in, " ")
		if got := deriveOperationID(method, path); got != want {
			t.Errorf("deriveOperationID(%q) = %q, want %q", in, got, want)
		}
	}
}

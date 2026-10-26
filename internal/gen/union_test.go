package gen

import (
	"strings"
	"testing"
)

// unionSpec has a union of each kind the generator models: one with a
// discriminator, one told apart by shape, a nullable one of scalars and an
// array, and a union of one beside null, which is no union at all.
const unionSpec = `
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /pets:
    post:
      operationId: addPet
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/Order' }
      responses: { "204": { description: ok } }
components:
  schemas:
    Cat:
      type: object
      required: [pet_type, lives]
      properties:
        pet_type: { type: string, enum: [cat] }
        lives: { type: integer, maximum: 9 }
    Dog:
      type: object
      required: [pet_type]
      properties:
        pet_type: { type: string }
        bark: { type: string, minLength: 2 }
    Pet:
      description: A pet of any kind.
      oneOf:
        - $ref: '#/components/schemas/Cat'
        - $ref: '#/components/schemas/Dog'
      discriminator:
        propertyName: pet_type
    FunctionTool:
      type: object
      required: [type, name]
      properties:
        type: { type: string, enum: [function] }
        name: { type: string }
    SearchTool:
      type: object
      required: [type]
      properties:
        type: { type: string, const: search }
    Tool:
      oneOf:
        - $ref: '#/components/schemas/FunctionTool'
        - $ref: '#/components/schemas/SearchTool'
        - type: string
          enum: [auto, none]
    Order:
      type: object
      required: [pet, tools]
      properties:
        pet: { $ref: '#/components/schemas/Pet' }
        tools:
          type: array
          items: { $ref: '#/components/schemas/Tool' }
        stop:
          oneOf:
            - type: string
              maxLength: 3
            - type: array
              items: { type: string }
            - type: 'null'
        note:
          anyOf:
            - type: string
            - type: 'null'
`

func TestUnionsDecodeAndValidate(t *testing.T) {
	out, err := generate(t, securedService(unionSpec))
	if err != nil {
		t.Fatal(err)
	}
	types := normalize(out["gen/api/types.gen.go"])
	for _, want := range []string{
		"type Pet struct { Cat *Cat Dog *Dog",
		"type Tool struct { FunctionTool *FunctionTool " +
			"SearchTool *SearchTool StringEnum *ToolStringEnum",
		"Stop optional.Optional[OrderStop]",
		"type OrderStop struct { String *string StringList *[]string",
		// A union of one beside null is that one, made optional.
		"Note optional.Optional[string]",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types should contain %q:\n%s", want, types)
		}
	}
	passes(t, out, map[string]string{
		"gen/api/union_test.go": `package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

// check decodes body into an Order and returns its validation errors as
// "type at loc" strings.
func check(t *testing.T, body string) (Order, []string) {
	t.Helper()
	var o Order
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	v := validate.New(validate.Body)
	o.Validate(v)
	var errs []string
	for _, e := range v.Errors() {
		errs = append(errs, fmt.Sprintf("%s at %v", e.Type, e.Loc))
	}
	return o, errs
}

func TestDiscriminator(t *testing.T) {
	o, errs := check(t, ` + "`" + `{"pet": {"pet_type": "cat", "lives": 12},
		"tools": []}` + "`" + `)
	if o.Pet.Cat == nil || o.Pet.Cat.Lives != 12 {
		t.Fatalf("pet = %+v, want the cat", o.Pet)
	}
	// The chosen variant is validated: a cat has at most nine lives.
	if fmt.Sprint(errs) != "[less_than_equal at [body pet lives]]" {
		t.Errorf("errors = %v", errs)
	}

	// Dog has no fixed pet_type, so its tag is its schema name.
	o, errs = check(t, ` + "`" + `{"pet": {"pet_type": "Dog", "bark": "w"},
		"tools": []}` + "`" + `)
	if o.Pet.Dog == nil {
		t.Fatalf("pet = %+v, want the dog", o.Pet)
	}
	if fmt.Sprint(errs) != "[string_too_short at [body pet bark]]" {
		t.Errorf("errors = %v", errs)
	}

	_, errs = check(t, ` + "`" + `{"pet": {"pet_type": "bird"}, "tools": []}` +
			"`" + `)
	if fmt.Sprint(errs) != "[union_tag_invalid at [body pet]]" {
		t.Errorf("errors = %v", errs)
	}
	_, errs = check(t, ` + "`" + `{"pet": {}, "tools": []}` + "`" + `)
	if fmt.Sprint(errs) != "[union_tag_not_found at [body pet]]" {
		t.Errorf("errors = %v", errs)
	}
}

func TestShapes(t *testing.T) {
	o, errs := check(t, ` + "`" + `{"pet": {"pet_type": "Dog"}, "tools": [
		{"type": "function", "name": "f"}, {"type": "search"}, "auto",
		{"type": "other"}, "sometimes"]}` + "`" + `)
	tools := o.Tools
	if tools[0].FunctionTool == nil || tools[0].FunctionTool.Name != "f" {
		t.Errorf("tools[0] = %+v, want the function", tools[0])
	}
	if tools[1].SearchTool == nil {
		t.Errorf("tools[1] = %+v, want the search", tools[1])
	}
	if e := tools[2].StringEnum; e == nil || *e != ToolStringEnumAuto {
		t.Errorf("tools[2] = %+v, want auto", tools[2])
	}
	want := "[union_no_match at [body tools 3] " +
		"union_no_match at [body tools 4]]"
	if fmt.Sprint(errs) != want {
		t.Errorf("errors = %v, want %s", errs, want)
	}
}

func TestNullableUnion(t *testing.T) {
	o, errs := check(t, ` + "`" + `{"pet": {"pet_type": "Dog"}, "tools": [],
		"stop": "long", "note": null}` + "`" + `)
	stop, ok := o.Stop.Get()
	if !ok || stop.String == nil {
		t.Fatalf("stop = %+v, want the string", o.Stop)
	}
	if fmt.Sprint(errs) != "[string_too_long at [body stop]]" {
		t.Errorf("errors = %v", errs)
	}
	if !o.Note.IsNull() {
		t.Errorf("note = %+v, want null", o.Note)
	}

	o, _ = check(t, ` + "`" + `{"pet": {"pet_type": "Dog"}, "tools": [],
		"stop": ["a", "b"]}` + "`" + `)
	if stop, _ := o.Stop.Get(); stop.StringList == nil ||
		len(*stop.StringList) != 2 {
		t.Errorf("stop = %+v, want the list", o.Stop)
	}
}

func TestMarshal(t *testing.T) {
	lives := Cat{PetType: CatPetTypeCat, Lives: 3}
	b, err := json.Marshal(Pet{Cat: &lives})
	if err != nil || string(b) != ` + "`" + `{"pet_type":"cat","lives":3}` +
			"`" + ` {
		t.Errorf("Marshal = %s, %v", b, err)
	}
	if _, err := json.Marshal(Pet{}); err == nil {
		t.Error("an empty union should not encode")
	}
}
`,
	})
}

func TestUnionRejections(t *testing.T) {
	for name, tc := range map[string]struct{ schema, want string }{
		"with properties": {`
      type: object
      properties: { a: { type: string } }
      oneOf: [ { type: string }, { type: integer } ]`,
			"combines oneOf/anyOf with properties"},
		"both keywords": {`
      oneOf: [ { type: string }, { type: integer } ]
      anyOf: [ { type: string }, { type: boolean } ]`,
			"both oneOf and anyOf"},
		"inline member without a tag": {`
      oneOf:
        - type: object
          properties: { kind: { type: string } }
        - type: object
          properties: { kind: { type: string } }
      discriminator: { propertyName: kind }`,
			"an inline member has no discriminator value"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := generate(t, securedService(`
openapi: 3.1.0
info: { title: T, version: "1" }
paths:
  /x:
    post:
      operationId: x
      requestBody:
        content:
          application/json:
            schema: { $ref: '#/components/schemas/U' }
      responses: { "204": { description: ok } }
components:
  schemas:
    U:`+tc.schema+"\n"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

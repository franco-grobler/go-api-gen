package spec

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// node parses one YAML value.
func node(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	return doc.Content[0]
}

// value decodes a node, or YAML text, for comparison.
func value(t *testing.T, n *yaml.Node) any {
	t.Helper()
	var v any
	if err := n.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNullableTo31(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"type",
			`{type: string, nullable: true, minLength: 2}`,
			`{type: [string, "null"], minLength: 2}`,
		},
		{
			"type list",
			`{type: [string, integer], nullable: true}`,
			`{type: [string, integer, "null"]}`,
		},
		{
			"type already null",
			`{type: [string, "null"], nullable: true}`,
			`{type: [string, "null"]}`,
		},
		{
			"false is dropped",
			`{type: string, nullable: false, description: d}`,
			`{type: string, description: d}`,
		},
		{
			"enum takes null too",
			`{type: string, enum: [a, b], nullable: true}`,
			`{type: [string, "null"], enum: [a, b, null]}`,
		},
		{
			"enum already null",
			`{type: string, enum: [a, null], nullable: true}`,
			`{type: [string, "null"], enum: [a, null]}`,
		},
		{
			"enum without type",
			`{enum: [a, b], nullable: true}`,
			`{enum: [a, b, null]}`,
		},
		{
			"oneOf gains a null member",
			`{oneOf: [{$ref: '#/A'}, {$ref: '#/B'}], nullable: true,
			  discriminator: {propertyName: kind}}`,
			`{oneOf: [{$ref: '#/A'}, {$ref: '#/B'}, {type: "null"}],
			  discriminator: {propertyName: kind}}`,
		},
		{
			"anyOf gains a null member",
			`{anyOf: [{$ref: '#/A'}, {type: string}], nullable: true}`,
			`{anyOf: [{$ref: '#/A'}, {type: string}, {type: "null"}]}`,
		},
		{
			"type beside oneOf takes null in both",
			`{type: object, oneOf: [{$ref: '#/A'}], nullable: true}`,
			`{type: [object, "null"], oneOf: [{$ref: '#/A'}, {type: "null"}]}`,
		},
		{
			"allOf of one is its member",
			`{allOf: [{$ref: '#/A'}], nullable: true, description: d}`,
			`{description: d, anyOf: [{$ref: '#/A'}, {type: "null"}]}`,
		},
		{
			"allOf with a nullable member",
			`{allOf: [{$ref: '#/A'}, {nullable: true}]}`,
			`{anyOf: [{$ref: '#/A'}, {type: "null"}]}`,
		},
		{
			"allOf with an annotated nullable member",
			`{allOf: [{$ref: '#/A'}, {nullable: true, description: m}]}`,
			`{description: m, anyOf: [{$ref: '#/A'}, {type: "null"}]}`,
		},
		{
			"allOf with a member saying false",
			`{allOf: [{$ref: '#/A'}, {nullable: false}]}`,
			`{allOf: [{$ref: '#/A'}]}`,
		},
		{
			"allOf of several",
			`{allOf: [{$ref: '#/A'}, {$ref: '#/B'}], nullable: true,
			  title: t, x-go-name: AB}`,
			`{title: t, anyOf: [{allOf: [{$ref: '#/A'}, {$ref: '#/B'}],
			  x-go-name: AB}, {type: "null"}]}`,
		},
		{
			// allOf still refuses null, so a type list alone would not do.
			"type beside allOf",
			`{type: object, allOf: [{$ref: '#/A'}], nullable: true,
			  readOnly: true}`,
			`{readOnly: true, anyOf: [{type: object, allOf: [{$ref: '#/A'}]},
			  {type: "null"}]}`,
		},
		{
			"not",
			`{type: string, not: {enum: [x]}, nullable: true}`,
			`{anyOf: [{type: string, not: {enum: [x]}}, {type: "null"}]}`,
		},
		{
			// Without type, JSON Schema already lets null past properties;
			// the anyOf says so in the way the loader reads as nullable.
			"no type",
			`{properties: {a: {type: string}}, nullable: true}`,
			`{anyOf: [{properties: {a: {type: string}}}, {type: "null"}]}`,
		},
		{
			"nothing but nullable",
			`{nullable: true, description: d}`,
			`{description: d, anyOf: [{}, {type: "null"}]}`,
		},
		{
			"not nullable",
			`{type: string, description: d}`,
			`{type: string, description: d}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := node(t, tt.in)
			before := value(t, in)
			got := value(t, nullableTo31(in))
			if want := value(t, node(t, tt.want)); !reflect.DeepEqual(
				got,
				want,
			) {
				t.Errorf("nullableTo31(%s)\n got %v\nwant %v", tt.in, got, want)
			}
			// The source is cached by the loader and copied again by every
			// later bundle, so it must be left as it was.
			if after := value(t, in); !reflect.DeepEqual(before, after) {
				t.Errorf("nullableTo31 changed its input to %v", after)
			}
		})
	}
}

// The translation renders as the JSON Schema a reader expects, and leaves
// "null" quoted, so it stays the type name rather than becoming a null.
func TestNullableTo31Renders(t *testing.T) {
	out, err := yaml.Marshal(nullableTo31(node(t, `
type: string
nullable: true
enum: [a]
`)))
	if err != nil {
		t.Fatal(err)
	}
	want := "type: [string, \"null\"]\nenum: [a, null]\n"
	if string(out) != want {
		t.Errorf("rendered\n%s\nwant\n%s", out, want)
	}
}

package gen

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// openAISpec is OpenAI's published document, fetched by
// `just update-openai-spec`: a large real-world specification the generated
// code has to compile against.
var openAISpec = filepath.Join("..", "..", "testdata", "openai",
	"openapi.yaml")

// OpenAI's specification generates a server that compiles, once patched for
// what the generator rejects on purpose: the document's own mistakes, which
// are not valid OpenAPI 3.0, and oneOf/anyOf, which is not supported yet.
// Each patch is something the generator refuses with a clear error, so a
// patch that becomes unnecessary is a feature that landed.
func TestOpenAISpecCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("generates and compiles a 40,000-line specification")
	}
	src, err := os.ReadFile(openAISpec)
	if err != nil {
		t.Skipf("%v; run `just update-openai-spec`", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		t.Fatal(err)
	}
	patchOpenAI(&doc)
	patched, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}

	out, err := generate(t, map[string]string{
		"go-api-gen.yaml": "version: 1\n" +
			"services:\n" +
			"  openai:\n" +
			"    spec: api.yaml\n" +
			"    to: gen/api\n" +
			"    generate: [types, validate, server]\n" +
			"examples:\n" +
			"  path: x-oaiMeta.examples\n" +
			"  request: request.curl\n",
		"api.yaml": string(patched),
	})
	if err != nil {
		t.Fatal(err)
	}
	goCommand(t, out, nil, "vet", "./...")
}

// patchOpenAI rewrites what the generator rejects in OpenAI's document.
func patchOpenAI(doc *yaml.Node) {
	walkMaps(doc, func(m *yaml.Node) {
		switch {
		// The generator does not support unions yet.
		case mapHas(m, "oneOf") || mapHas(m, "anyOf"):
			mapDel(m, "oneOf", "anyOf", "discriminator")
			mapSet(m, "x-go-type", "json.RawMessage")
			mapSet(m, "x-go-import", "encoding/json")

		// A 3.0 author marks a reference nullable beside it, where the
		// keyword is ignored; allOf is where it takes effect.
		case mapHas(m, "$ref") && mapHas(m, "nullable"):
			ref := mapVal(m, "$ref")
			mapDel(m, "$ref")
			wrapped := &yaml.Node{Kind: yaml.MappingNode}
			mapSetNode(wrapped, "$ref", ref)
			mapSetNode(m, "allOf",
				&yaml.Node{Kind: yaml.SequenceNode,
					Content: []*yaml.Node{wrapped}})

		// $recursiveRef is JSON Schema 2019-09, not OpenAPI 3.0. The one
		// use points CompoundFilter at itself.
		case mapHas(m, "$recursiveRef"):
			mapDel(m, "$recursiveRef")
			mapSet(m, "$ref", "#/components/schemas/CompoundFilter")
		}
		// A type beside $ref is redundant, and ignored by OpenAPI 3.0.
		if mapHas(m, "$ref") {
			mapDel(m, "type")
		}
		mapRename(m, "min_items", "minItems")
		mapRename(m, "max_items", "maxItems")
		// Not OpenAPI 3.0 keywords at all.
		mapDel(m, "$recursiveAnchor", "propertyNames")
		if v := mapVal(m, "optional"); v != nil && mapHas(m, "type") {
			mapDel(m, "optional")
		}
	})
	fixPathParams(mapVal(doc.Content[0], "paths"))
}

// fixPathParams renames a path parameter the template calls something else
// and declares those the template names but no operation declares, both of
// which OpenAI's certificate endpoints do.
func fixPathParams(paths *yaml.Node) {
	tmpl := regexp.MustCompile(`\{([^}]+)\}`)
	for i := 0; i+1 < len(paths.Content); i += 2 {
		path, item := paths.Content[i].Value, paths.Content[i+1]
		var names []string
		for _, m := range tmpl.FindAllStringSubmatch(path, -1) {
			names = append(names, m[1])
		}
		for j := 0; j+1 < len(item.Content); j += 2 {
			op := item.Content[j+1]
			if op.Kind != yaml.MappingNode || !mapHas(op, "responses") {
				continue
			}
			params := mapVal(op, "parameters")
			if params == nil {
				params = &yaml.Node{Kind: yaml.SequenceNode}
				mapSetNode(op, "parameters", params)
			}
			declared := map[string]bool{}
			for _, p := range params.Content {
				if mapStr(p, "in") != "path" {
					continue
				}
				if mapStr(p, "name") == "cert_id" {
					mapSet(p, "name", "certificate_id")
				}
				declared[mapStr(p, "name")] = true
			}
			for _, n := range names {
				if declared[n] {
					continue
				}
				p := &yaml.Node{Kind: yaml.MappingNode}
				mapSet(p, "name", n)
				mapSet(p, "in", "path")
				mapSetNode(p, "required", &yaml.Node{Kind: yaml.ScalarNode,
					Tag: "!!bool", Value: "true"})
				schema := &yaml.Node{Kind: yaml.MappingNode}
				mapSet(schema, "type", "string")
				mapSetNode(p, "schema", schema)
				params.Content = append(params.Content, p)
			}
		}
	}
}

func walkMaps(n *yaml.Node, fn func(*yaml.Node)) {
	if n.Kind == yaml.MappingNode {
		fn(n)
	}
	for _, c := range n.Content {
		walkMaps(c, fn)
	}
}

func mapVal(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapHas(m *yaml.Node, key string) bool { return mapVal(m, key) != nil }

func mapStr(m *yaml.Node, key string) string {
	if v := mapVal(m, key); v != nil {
		return v.Value
	}
	return ""
}

func mapDel(m *yaml.Node, keys ...string) {
	for i := 0; i+1 < len(m.Content); {
		if slices.Contains(keys, m.Content[i].Value) {
			m.Content = slices.Delete(m.Content, i, i+2)
			continue
		}
		i += 2
	}
}

func mapSet(m *yaml.Node, key, value string) {
	mapSetNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Value: value})
}

func mapSetNode(m *yaml.Node, key string, value *yaml.Node) {
	if v := mapVal(m, key); v != nil {
		*v = *value
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
}

func mapRename(m *yaml.Node, from, to string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == from {
			m.Content[i].Value = to
		}
	}
}

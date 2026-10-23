package spec

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// examplesOf parses one operation and reads its examples at keys.
func examplesOf(
	t *testing.T, op string, keys *ExampleKeys,
) []OperationExample {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(op), &root); err != nil {
		t.Fatal(err)
	}
	o := &Operation{node: root.Content[0]}
	return o.Examples(keys)
}

var oaiKeys = &ExampleKeys{
	Path:     []string{"x-oaiMeta", "examples"},
	Title:    []string{"title"},
	Request:  []string{"request", "curl"},
	Response: []string{"response"},
}

func TestExamplesReadACurlBody(t *testing.T) {
	got := examplesOf(t, `
x-oaiMeta:
  examples:
    title: Default
    request:
      curl: |
        curl https://api.example.com/pets \
          -H "Content-Type: application/json" \
          -d '{"name": "Rex", "age": 1.0}'
    response: '{"id": "pet_1"}'
`, oaiKeys)
	if len(got) != 1 {
		t.Fatalf("got %d examples, want 1 from a single mapping", len(got))
	}
	want := OperationExample{
		Title:    "Default",
		Request:  "{\n  \"name\": \"Rex\",\n  \"age\": 1.0\n}",
		Response: "{\n  \"id\": \"pet_1\"\n}",
	}
	if got[0] != want {
		t.Errorf("got %+v\nwant %+v", got[0], want)
	}
}

// Text that is not JSON is still an example worth showing: a curl command
// with no body still names the query string, and a stream is a stream.
func TestExamplesKeepTextThatIsNotJSON(t *testing.T) {
	got := examplesOf(t, `
x-oaiMeta:
  examples:
    - title: List
      request:
        curl: curl "https://api.example.com/pets?limit=2"
      response: |
        data: {"delta": "a"}

        data: [DONE]
    - title: Empty
`, oaiKeys)
	if len(got) != 1 {
		t.Fatalf("got %d examples, want 1: an empty one is dropped", len(got))
	}
	if !strings.HasPrefix(got[0].Request, "curl ") {
		t.Errorf("request = %q, want the curl command", got[0].Request)
	}
	if !strings.HasSuffix(got[0].Response, "data: [DONE]") {
		t.Errorf("response = %q, want the stream as written", got[0].Response)
	}
}

func TestExamplesMissingPathIsNone(t *testing.T) {
	if got := examplesOf(t, "summary: x\n", oaiKeys); got != nil {
		t.Errorf("got %+v, want none", got)
	}
	if got := examplesOf(t, "summary: x\n", &ExampleKeys{}); got != nil {
		t.Errorf("got %+v, want none without a path", got)
	}
}

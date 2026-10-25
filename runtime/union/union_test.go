package union

import (
	"strings"
	"testing"
)

// Two objects told apart by the value of their "type" property, the way most
// of OpenAI's unions are, and a string beside them.
var tools = []Shape{
	{
		Name: "FunctionTool", Kinds: Object,
		Required:   []string{"type", "name"},
		Properties: []string{"type", "name", "parameters"},
		Fixed:      []Fixed{{Property: "type", Values: []string{`"function"`}}},
	},
	{
		Name: "FileSearchTool", Kinds: Object,
		Required:   []string{"type"},
		Properties: []string{"type", "vector_store_ids"},
		Fixed: []Fixed{{
			Property: "type", Values: []string{`"file_search"`},
		}},
	},
	{Name: "string", Kinds: String},
}

func TestChoose(t *testing.T) {
	for _, tc := range []struct {
		data string
		mode Mode
		want int
		slug string
	}{
		{`{"type": "function", "name": "f"}`, OneOf, 0, ""},
		{`{"type":"file_search"}`, OneOf, 1, ""},
		{`"auto"`, OneOf, 2, ""},
		// The required name is missing, and the type rules out the other.
		{`{"type": "function"}`, OneOf, -1, "union_no_match"},
		{`{"type": "code_interpreter"}`, OneOf, -1, "union_no_match"},
		{`42`, OneOf, -1, "union_no_match"},
		{`null`, OneOf, -1, "union_no_match"},
	} {
		got, problem := Choose([]byte(tc.data), tc.mode, tools)
		slug := ""
		if problem != nil {
			slug = problem.Type
		}
		if got != tc.want || slug != tc.slug {
			t.Errorf("Choose(%s) = %d, %q; want %d, %q",
				tc.data, got, slug, tc.want, tc.slug)
		}
	}
}

// Without a tag, the variant declaring more of the object's keys wins; a tie
// is ambiguous under oneOf, and goes to the first variant under anyOf.
func TestChooseMostSpecific(t *testing.T) {
	shapes := []Shape{
		{Name: "Text", Kinds: Object, Required: []string{"text"},
			Properties: []string{"text"}},
		{Name: "Verbose", Kinds: Object, Required: []string{"text"},
			Properties: []string{"text", "language", "duration"}},
	}
	if got, p := Choose([]byte(`{"text":"hi","language":"en"}`), OneOf,
		shapes); got != 1 || p != nil {
		t.Errorf("verbose payload: got %d, %v; want 1", got, p)
	}
	_, p := Choose([]byte(`{"text":"hi"}`), OneOf, shapes)
	if p == nil || p.Type != "union_ambiguous" {
		t.Errorf("tie under oneOf: problem %v; want union_ambiguous", p)
	}
	if got, p := Choose([]byte(`{"text":"hi"}`), AnyOf, shapes); got != 0 ||
		p != nil {
		t.Errorf("tie under anyOf: got %d, %v; want 0", got, p)
	}
}

func TestChooseScalars(t *testing.T) {
	shapes := []Shape{
		{Name: "number", Kinds: Number},
		{Name: "integer", Kinds: Integer},
		{Name: "Mode", Kinds: String, Values: []string{`"auto"`, `"none"`}},
		{Name: "string", Kinds: String},
		{Name: "array", Kinds: Array},
		{Name: "closed", Kinds: Object, Closed: true,
			Properties: []string{"a"}},
	}
	for data, want := range map[string]int{
		`3`:          1, // an integer is more an integer than a number
		`3.5`:        0,
		`1e3`:        0,
		` "none" `:   2, // a listed value is more specific than any string
		`"other"`:    3,
		`["a", "b"]`: 4,
		`{"a": 1}`:   5,
	} {
		if got, p := Choose([]byte(data), OneOf, shapes); got != want {
			t.Errorf("Choose(%s) = %d, %v; want %d", data, got, p, want)
		}
	}
	if _, p := Choose([]byte(`{"a":1,"b":2}`), OneOf, shapes); p == nil {
		t.Error("a closed shape accepted an undeclared key")
	}
	if _, p := Choose([]byte(`true`), OneOf, shapes); p == nil ||
		!strings.Contains(p.Msg, "number, integer, Mode") {
		t.Errorf("no match should list the variants: %v", p)
	}
}

func TestTag(t *testing.T) {
	data := []byte(`{"pet_type":"cat","name":"x"}`)
	if tag, p := Tag(data, "pet_type"); tag != "cat" || p != nil {
		t.Errorf("Tag = %q, %v; want cat", tag, p)
	}
	for _, data := range []string{`{"name":"x"}`, `{"pet_type":1}`, `"cat"`} {
		_, p := Tag([]byte(data), "pet_type")
		if p == nil || p.Type != "union_tag_not_found" {
			t.Errorf("Tag(%s) problem = %v; want union_tag_not_found", data, p)
		}
	}
	p := UnknownTag("bird", "pet_type", []string{"cat", "dog"})
	want := "Input tag 'bird' found using 'pet_type' does not match any of " +
		"the expected tags: 'cat', 'dog'"
	if p.Type != "union_tag_invalid" || p.Msg != want {
		t.Errorf("UnknownTag = %+v", p)
	}
}

func TestIsNull(t *testing.T) {
	if !IsNull([]byte(" null\n")) || IsNull([]byte(`"null"`)) {
		t.Error("IsNull misjudged its input")
	}
}

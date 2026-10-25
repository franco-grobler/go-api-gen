package spec

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sample is an example value, rendered for documentation: indented JSON when
// the value is JSON, and the text as written when it is not.
type Sample struct {
	Title string // the name or summary it was given, if any
	Text  string
}

// OperationExample is an example exchange found at a configured location in
// an operation, for a document that keeps examples somewhere the standard
// does not, as OpenAI's does under x-oaiMeta. Either side may be empty.
type OperationExample struct {
	Title    string
	Request  string
	Response string
}

// ExampleKeys locates examples inside an operation object. Each field is a
// path of mapping keys and sequence indexes.
type ExampleKeys struct {
	Path     []string // from the operation to an example or a list of them
	Title    []string // from each example to its title
	Request  []string // from each example to its request
	Response []string // from each example to its response
}

// Examples returns the examples found at keys, in document order. A path that
// leads to one example rather than a list of them is a list of one.
func (o *Operation) Examples(keys *ExampleKeys) []OperationExample {
	if len(keys.Path) == 0 {
		return nil
	}
	at := follow(o.node, keys.Path)
	if at == nil {
		return nil
	}
	items := []*yaml.Node{at}
	if at.Kind == yaml.SequenceNode {
		items = at.Content
	}
	var out []OperationExample
	for _, item := range items {
		ex := OperationExample{
			Request:  render(follow(item, keys.Request), true),
			Response: render(follow(item, keys.Response), false),
		}
		title := follow(item, keys.Title)
		if title != nil && title.Kind == yaml.ScalarNode {
			ex.Title = title.Value
		}
		if ex.Request != "" || ex.Response != "" {
			out = append(out, ex)
		}
	}
	return out
}

// follow walks keys from n, returning nil when any step is missing.
func follow(n *yaml.Node, keys []string) *yaml.Node {
	n = unwrap(n)
	for _, k := range keys {
		switch {
		case n == nil:
			return nil
		case n.Kind == yaml.MappingNode:
			n = unwrap(mapGet(n, k))
		case n.Kind == yaml.SequenceNode:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(n.Content) {
				return nil
			}
			n = unwrap(n.Content[i])
		default:
			return nil
		}
	}
	return n
}

// mediaExamples reads a media type's example, or its examples map in order.
// An externalValue is not fetched, so such an entry is skipped.
func (l *Loader) mediaExamples(file string, n *yaml.Node) ([]Sample, error) {
	if ex := mapGet(n, "example"); ex != nil {
		if text := render(ex, false); text != "" {
			return []Sample{{Text: text}}, nil
		}
		return nil, nil
	}
	var out []Sample
	err := mapEach(mapGet(n, "examples"), func(
		name string, _, v *yaml.Node,
	) error {
		efile, en, _, err := l.deref(file, v)
		if err != nil {
			return err
		}
		f := &fields{file: efile}
		title := f.str(mapGet(en, "summary"))
		if f.err != nil {
			return f.err
		}
		if title == "" {
			title = name
		}
		if text := render(mapGet(en, "value"), false); text != "" {
			out = append(out, Sample{Title: title, Text: text})
		}
		return nil
	})
	return out, err
}

// render turns an example value into documentation text. A mapping or a
// sequence is written as JSON in its original key order, and a string that
// holds JSON is reindented. A request may also be a curl command, whose -d
// body is used when it is JSON, since that is what the handler receives.
func render(n *yaml.Node, request bool) string {
	n = unwrap(n)
	if n == nil {
		return ""
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		var buf bytes.Buffer
		if err := writeJSON(&buf, n); err != nil {
			return ""
		}
		return indentJSON(buf.Bytes())
	}
	text := strings.TrimSpace(n.Value)
	if s := indentJSON([]byte(text)); s != "" {
		return s
	}
	if request {
		if s := indentJSON([]byte(curlBody(text))); s != "" {
			return s
		}
	}
	return text
}

// curlBody returns the single-quoted argument of a curl command's -d flag, or
// "" when it has none.
func curlBody(cmd string) string {
	for _, flag := range []string{"-d '", "--data '", "--data-raw '"} {
		_, rest, ok := strings.Cut(cmd, flag)
		if !ok {
			continue
		}
		if body, _, ok := strings.Cut(rest, "'"); ok {
			return body
		}
	}
	return ""
}

// indentJSON reindents src, or returns "" when it is not JSON. Only objects
// and arrays count: a bare string or number is text, not a payload.
func indentJSON(src []byte) string {
	src = bytes.TrimSpace(src)
	if len(src) == 0 || (src[0] != '{' && src[0] != '[') {
		return ""
	}
	var compact, out bytes.Buffer
	if err := json.Compact(&compact, src); err != nil {
		return ""
	}
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return ""
	}
	return out.String()
}

// writeJSON encodes a node as compact JSON, keeping mapping keys in document
// order, which decoding into a Go map would lose.
func writeJSON(buf *bytes.Buffer, n *yaml.Node) error {
	n = unwrap(n)
	switch n.Kind {
	case yaml.MappingNode:
		buf.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				buf.WriteByte(',')
			}
			key, err := json.Marshal(n.Content[i].Value)
			if err != nil {
				return err
			}
			buf.Write(key)
			buf.WriteByte(':')
			if err := writeJSON(buf, n.Content[i+1]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for i, e := range n.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSON(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		v, err := anyVal(n)
		if err != nil {
			return err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	return nil
}

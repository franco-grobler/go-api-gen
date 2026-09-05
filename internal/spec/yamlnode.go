package spec

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// pos is a source location used to make every diagnostic clickable.
type pos struct {
	File string
	Line int
	Col  int
}

func at(file string, n *yaml.Node) pos {
	if n == nil {
		return pos{File: file}
	}
	return pos{File: file, Line: n.Line, Col: n.Column}
}

func (p pos) String() string {
	if p.Line == 0 {
		return p.File
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// errf builds an error prefixed with a source location.
func errf(p pos, format string, args ...any) error {
	return fmt.Errorf("%s: %s", p, fmt.Sprintf(format, args...))
}

// unwrap follows YAML aliases so callers always see the target node.
func unwrap(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// isMap reports whether n is a mapping node.
func isMap(n *yaml.Node) bool {
	n = unwrap(n)
	return n != nil && n.Kind == yaml.MappingNode
}

// mapGet returns the value node for key in a mapping node, or nil.
func mapGet(n *yaml.Node, key string) *yaml.Node {
	n = unwrap(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if unwrap(n.Content[i]).Value == key {
			return unwrap(n.Content[i+1])
		}
	}
	return nil
}

// mapEach calls fn for each key/value pair in document order. Mapping order is
// preserved throughout the loader so generated output stays deterministic and
// matches the order the author wrote.
func mapEach(
	n *yaml.Node,
	fn func(key string, keyNode, val *yaml.Node) error,
) error {
	n = unwrap(n)
	if n == nil {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected a mapping, found %s", kindName(n))
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := unwrap(n.Content[i])
		if err := fn(k.Value, k, unwrap(n.Content[i+1])); err != nil {
			return err
		}
	}
	return nil
}

// seqEach calls fn for each element of a sequence node.
func seqEach(n *yaml.Node, fn func(i int, val *yaml.Node) error) error {
	n = unwrap(n)
	if n == nil {
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("expected a sequence, found %s", kindName(n))
	}
	for i, c := range n.Content {
		if err := fn(i, unwrap(c)); err != nil {
			return err
		}
	}
	return nil
}

func kindName(n *yaml.Node) string {
	switch unwrap(n).Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a sequence"
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.DocumentNode:
		return "a document"
	default:
		return "an unknown node"
	}
}

// strVal reads a scalar string, erroring on any other kind.
func strVal(file string, n *yaml.Node) (string, error) {
	n = unwrap(n)
	if n == nil {
		return "", nil
	}
	if n.Kind != yaml.ScalarNode {
		return "", errf(at(file, n), "expected a string, found %s", kindName(n))
	}
	return n.Value, nil
}

// boolVal reads a scalar bool.
func boolVal(file string, n *yaml.Node) (bool, error) {
	n = unwrap(n)
	if n == nil {
		return false, nil
	}
	b, err := strconv.ParseBool(n.Value)
	if err != nil {
		return false, errf(
			at(file, n),
			"expected true or false, found %q",
			n.Value,
		)
	}
	return b, nil
}

// uintVal reads a non-negative integer, as used by minLength, maxItems, and
// friends. It returns a pointer so an absent keyword stays distinguishable
// from an explicit zero.
func uintVal(file string, n *yaml.Node) (*uint64, error) {
	n = unwrap(n)
	if n == nil {
		return nil, nil
	}
	v, err := strconv.ParseUint(n.Value, 10, 64)
	if err != nil {
		return nil, errf(
			at(file, n),
			"expected a non-negative integer, found %q",
			n.Value,
		)
	}
	return &v, nil
}

// numVal reads a number, as used by minimum, maximum, and multipleOf.
func numVal(file string, n *yaml.Node) (*float64, error) {
	n = unwrap(n)
	if n == nil {
		return nil, nil
	}
	v, err := strconv.ParseFloat(n.Value, 64)
	if err != nil {
		return nil, errf(at(file, n), "expected a number, found %q", n.Value)
	}
	return &v, nil
}

// strsVal reads either a single scalar or a sequence of scalars. OpenAPI 3.1
// allows "type" to be either.
func strsVal(file string, n *yaml.Node) ([]string, error) {
	n = unwrap(n)
	if n == nil {
		return nil, nil
	}
	if n.Kind == yaml.ScalarNode {
		return []string{n.Value}, nil
	}
	var out []string
	err := seqEach(n, func(_ int, v *yaml.Node) error {
		s, err := strVal(file, v)
		if err != nil {
			return err
		}
		out = append(out, s)
		return nil
	})
	if err != nil {
		return nil, errf(at(file, n), "%v", err)
	}
	return out, nil
}

// anyVal decodes an arbitrary node into a plain Go value, for example, default
// and enum entries.
func anyVal(n *yaml.Node) (any, error) {
	n = unwrap(n)
	if n == nil {
		return nil, nil
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// fields reads the optional scalar fields of one object, remembering the first
// error rather than returning one from every call.
//
// A malformed value here, an info.title written as a mapping, say, is worth
// reporting: silently reading it as the empty string hides a real mistake in
// the document. Recording the error keeps the call sites readable while still
// surfacing it.
type fields struct {
	file string
	err  error
}

// str reads an optional string field.
func (r *fields) str(n *yaml.Node) string {
	if r.err != nil {
		return ""
	}
	v, err := strVal(r.file, n)
	if err != nil {
		r.err = err
	}
	return v
}

// bool reads an optional boolean field.
func (r *fields) bool(n *yaml.Node) bool {
	if r.err != nil {
		return false
	}
	if n == nil {
		return false
	}
	v, err := boolVal(r.file, n)
	if err != nil {
		r.err = err
	}
	return v
}

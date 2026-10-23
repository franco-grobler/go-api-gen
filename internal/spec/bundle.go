package spec

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// BundleComponent is one schema to emit into the bundled document's
// components/schemas, under the given name.
type BundleComponent struct {
	Schema *Schema
	Name   string
}

// BundleOptions configures a bundle.
type BundleOptions struct {
	// Components are emitted in order. Every $ref that resolves to one of
	// these schemas is rewritten to point at it locally.
	Components []BundleComponent
}

// Bundle produces a self-contained OpenAPI document node for d.
//
// References that resolve to a listed component become local
// "#/components/schemas/Name" references; every other reference, including
// shared parameters, responses, and request bodies, is inlined. The result
// depends on no other file, which is what a gateway or a docs renderer needs,
// while the Go emitters separately keep shared schemas as imports rather than
// copies.
func (l *Loader) Bundle(d *Document, opts BundleOptions) (*yaml.Node, error) {
	root, err := l.File(d.File)
	if err != nil {
		return nil, err
	}

	b := &bundler{
		loader:   l,
		names:    map[Location]string{},
		inlining: map[Location]bool{},
	}
	for _, c := range opts.Components {
		if c.Schema.Loc.IsZero() {
			return nil, fmt.Errorf(
				"cannot bundle an inline schema as component %q",
				c.Name,
			)
		}
		b.names[c.Schema.Loc] = c.Name
	}

	out := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	emittedComponents := false

	// Preserve the author's top-level key order.
	err = mapEach(root, func(key string, keyNode, val *yaml.Node) error {
		switch key {
		case "components":
			comps, err := b.components(opts)
			if err != nil {
				return err
			}
			schemes, err := b.securitySchemes(d.File, val)
			if err != nil {
				return err
			}
			if schemes != nil {
				mapSet(comps, "securitySchemes", schemes)
			}
			mapSet(out, "components", comps)
			emittedComponents = true
		default:
			v, err := b.rewrite(d.File, val)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			mapSet(out, key, v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if !emittedComponents && len(opts.Components) > 0 {
		schemas, err := b.components(opts)
		if err != nil {
			return nil, err
		}
		mapSet(out, "components", schemas)
	}
	return out, nil
}

// MarshalBundle renders a bundled document node as YAML.
func MarshalBundle(n *yaml.Node) ([]byte, error) {
	return yaml.Marshal(n)
}

// YAML resolved tags used when synthesising nodes.
const (
	tagMap  = "!!map"
	tagSeq  = "!!seq"
	tagStr  = "!!str"
	tagNull = "!!null"
)

type bundler struct {
	loader   *Loader
	names    map[Location]string
	inlining map[Location]bool
}

// components builds the components mapping, holding only schemas. Shared
// parameters, responses, and request bodies are inlined at their use sites, so
// nothing else needs to survive into the bundle except security schemes, which
// the caller adds with securitySchemes.
func (b *bundler) components(opts BundleOptions) (*yaml.Node, error) {
	schemas := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	for _, c := range opts.Components {
		node, err := b.loader.nodeAt(c.Schema.Loc)
		if err != nil {
			return nil, err
		}
		// Rewrite in the defining file's context so the schema's own relative
		// references resolve from where it was authored, not from the service
		// spec that happens to be pulling it in.
		v, err := b.rewrite(c.Schema.Loc.File, node)
		if err != nil {
			return nil, fmt.Errorf("component %s: %w", c.Name, err)
		}
		mapSet(schemas, c.Name, v)
	}
	comps := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	mapSet(comps, "schemas", schemas)
	return comps, nil
}

// securitySchemes copies the securitySchemes of a components mapping, or
// returns nil when it declares none.
//
// A security requirement names its scheme rather than referencing it, so a
// scheme cannot be inlined at its use site the way a shared parameter is. It
// has to survive as a component, or every requirement naming it dangles.
func (b *bundler) securitySchemes(
	file string,
	components *yaml.Node,
) (*yaml.Node, error) {
	n := mapGet(components, "securitySchemes")
	if n == nil {
		return nil, nil
	}
	v, err := b.rewrite(file, n)
	if err != nil {
		return nil, fmt.Errorf("components securitySchemes: %w", err)
	}
	return v, nil
}

// rewrite deep-copies a node, resolving references as it goes.
func (b *bundler) rewrite(file string, n *yaml.Node) (*yaml.Node, error) {
	n = unwrap(n)
	if n == nil {
		return nil, nil
	}

	switch n.Kind {
	case yaml.MappingNode:
		if refNode := mapGet(n, "$ref"); refNode != nil {
			return b.rewriteRef(file, n, refNode)
		}
		out := &yaml.Node{Kind: yaml.MappingNode, Tag: n.Tag, Style: n.Style}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := copyNode(unwrap(n.Content[i]))
			v, err := b.rewrite(file, n.Content[i+1])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k.Value, err)
			}
			out.Content = append(out.Content, k, v)
		}
		return out, nil

	case yaml.SequenceNode:
		out := &yaml.Node{Kind: yaml.SequenceNode, Tag: n.Tag, Style: n.Style}
		for i, c := range n.Content {
			v, err := b.rewrite(file, c)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out.Content = append(out.Content, v)
		}
		return out, nil

	default:
		return copyNode(n), nil
	}
}

func (b *bundler) rewriteRef(
	file string,
	n,
	refNode *yaml.Node,
) (*yaml.Node, error) {
	ref, err := strVal(file, refNode)
	if err != nil {
		return nil, err
	}
	loc, err := parseRef(file, ref)
	if err != nil {
		return nil, errf(at(file, refNode), "%v", err)
	}

	// A reference to a bundled component becomes a local reference, keeping any
	// annotation siblings the author wrote at the reference site.
	if name, ok := b.names[loc]; ok {
		out := &yaml.Node{Kind: yaml.MappingNode, Tag: n.Tag, Style: n.Style}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := unwrap(n.Content[i])
			if k.Value == "$ref" {
				mapSet(out, "$ref", scalar("#/components/schemas/"+name))
				continue
			}
			v, err := b.rewrite(file, n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, copyNode(k), v)
		}
		return out, nil
	}

	// Everything else is inlined at the point of use.
	if b.inlining[loc] {
		return nil, errf(at(file, refNode),
			"$ref %q cannot be bundled: it forms a cycle through a node that "+
				"is not a schema component.\n"+
				"Promote it to an entry under components/schemas so it can be "+
				"referenced instead of inlined.", ref)
	}
	b.inlining[loc] = true
	defer delete(b.inlining, loc)

	target, err := b.loader.nodeAt(loc)
	if err != nil {
		return nil, errf(at(file, refNode), "%v", err)
	}
	inlined, err := b.rewrite(loc.File, target)
	if err != nil {
		return nil, err
	}
	// Annotations written beside the reference override the target's own.
	if isMap(inlined) {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := unwrap(n.Content[i])
			if k.Value == "$ref" {
				continue
			}
			v, err := b.rewrite(file, n.Content[i+1])
			if err != nil {
				return nil, err
			}
			mapSet(inlined, k.Value, v)
		}
	}
	return inlined, nil
}

// copyNode deep-copies a node, dropping anchors so the output never carries a
// duplicate anchor name from two different source files.
func copyNode(n *yaml.Node) *yaml.Node {
	n = unwrap(n)
	if n == nil {
		return nil
	}
	out := &yaml.Node{
		Kind:        n.Kind,
		Style:       n.Style,
		Tag:         n.Tag,
		Value:       n.Value,
		HeadComment: n.HeadComment,
		LineComment: n.LineComment,
		FootComment: n.FootComment,
	}
	for _, c := range n.Content {
		out.Content = append(out.Content, copyNode(c))
	}
	return out
}

// mapSet sets key to val in a mapping node, replacing an existing entry in
// place so that key order stays stable.
func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	if val == nil {
		val = &yaml.Node{Kind: yaml.ScalarNode, Tag: tagNull, Value: "null"}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if unwrap(m.Content[i]).Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, scalar(key), val)
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: v}
}

package spec

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// UnifyInfo is the info block of a unified document. The services being
// stitched together each have their own title and version, so the unified
// document needs its own rather than arbitrarily picking one.
type UnifyInfo struct {
	Title       string
	Version     string
	Description string
}

// UnifySource is one service document contributing to a unified spec.
type UnifySource struct {
	Doc *Document
	// Tag is added to any operation that declares no tags of its own, so a
	// docs renderer groups a merged document by service instead of showing
	// one flat list of every endpoint in the monorepo.
	Tag string
	// TagDescription describes Tag in the unified document's tag list.
	TagDescription string
}

// UnifyOptions configures a unified document.
type UnifyOptions struct {
	OpenAPI    string
	Info       UnifyInfo
	Servers    []Server
	Sources    []UnifySource
	Components []BundleComponent
}

// Unify stitches several service documents into one self-contained OpenAPI
// document.
//
// It is a different deliverable from the per-package bundles: those describe
// one service each, while this describes the platform as a whole, for a
// gateway, a docs portal, or a cross-cutting client. Paths from different
// services merge method by method, so two services may serve different methods
// of the same path, but not the same one.
func (l *Loader) Unify(opts *UnifyOptions) (*yaml.Node, error) {
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

	if err := checkOperationIDs(opts.Sources); err != nil {
		return nil, err
	}

	paths, tags, err := b.mergePaths(opts.Sources)
	if err != nil {
		return nil, err
	}

	out := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	openapi := opts.OpenAPI
	if openapi == "" {
		openapi = highestOpenAPI(opts.Sources)
	}
	mapSet(out, "openapi", scalar(openapi))
	mapSet(out, "info", infoNode(opts.Info))
	if len(opts.Servers) > 0 {
		mapSet(out, "servers", serversNode(opts.Servers))
	}
	if len(tags.Content) > 0 {
		mapSet(out, "tags", tags)
	}
	mapSet(out, "paths", paths)

	if len(opts.Components) > 0 {
		comps, err := b.components(BundleOptions{Components: opts.Components})
		if err != nil {
			return nil, err
		}
		mapSet(out, "components", comps)
	}
	return out, nil
}

// checkOperationIDs rejects an id used by two services. Each document has
// already been checked on its own; a clash only appears once they are merged,
// and it would break every client generator downstream.
func checkOperationIDs(sources []UnifySource) error {
	type owner struct {
		file   string
		method string
		path   string
	}
	seen := map[string]owner{}
	for _, src := range sources {
		for _, pi := range src.Doc.Paths {
			for _, op := range pi.Operations {
				if prev, dup := seen[op.OperationID]; dup {
					return fmt.Errorf("operationId %q is used by both %s %s "+
						"in %s and %s %s in %s.\n"+
						"Give one of them a distinct operationId so the "+
						"unified document has unique operations.",
						op.OperationID,
						prev.method, prev.path, prev.file,
						op.Method, op.Path, src.Doc.File)
				}
				seen[op.OperationID] = owner{src.Doc.File, op.Method, op.Path}
			}
		}
	}
	return nil
}

// mergePaths combines every source's paths into one mapping, and returns the
// tag list describing the services that contributed.
func (b *bundler) mergePaths(sources []UnifySource) (
	paths,
	tags *yaml.Node,
	err error,
) {
	paths = &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	tags = &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq}

	// path -> key -> the file that contributed it, for collision reporting.
	contributor := map[string]map[string]string{}
	tagged := map[string]bool{}

	for _, src := range sources {
		root, err := b.loader.File(src.Doc.File)
		if err != nil {
			return nil, nil, err
		}
		pathsNode := mapGet(root, "paths")

		for _, pi := range src.Doc.Paths {
			itemNode := mapGet(pathsNode, pi.Path)
			if itemNode == nil {
				return nil, nil, fmt.Errorf(
					"%s: path %q disappeared between parsing and bundling",
					src.Doc.File,
					pi.Path,
				)
			}
			item, err := b.rewrite(src.Doc.File, itemNode)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: paths %s: %w",
					src.Doc.File,
					pi.Path,
					err,
				)
			}
			if src.Tag != "" && applyTag(item, src.Tag) {
				if !tagged[src.Tag] {
					tagged[src.Tag] = true
					tags.Content = append(
						tags.Content,
						tagNode(src.Tag, src.TagDescription),
					)
				}
			}

			if contributor[pi.Path] == nil {
				contributor[pi.Path] = map[string]string{}
			}
			existing := mapGet(paths, pi.Path)
			if existing == nil {
				for i := 0; i+1 < len(item.Content); i += 2 {
					key := unwrap(item.Content[i]).Value
					contributor[pi.Path][key] = src.Doc.File
				}
				mapSet(paths, pi.Path, item)
				continue
			}
			// The path already exists, so merge key by key. Two services may
			// own different methods of one path; they may not own the same one.
			for i := 0; i+1 < len(item.Content); i += 2 {
				key := unwrap(item.Content[i]).Value
				if prev, dup := contributor[pi.Path][key]; dup {
					return nil, nil, fmt.Errorf("path %q defines %q in both "+
						"%s and %s.\n"+
						"Two services cannot contribute the same operation to "+
						"a unified document.",
						pi.Path, key, prev, src.Doc.File)
				}
				contributor[pi.Path][key] = src.Doc.File
				existing.Content = append(
					existing.Content,
					copyNode(unwrap(item.Content[i])),
					item.Content[i+1],
				)
			}
		}
	}
	return paths, tags, nil
}

// applyTag tags every operation of a path item that declares no tags, and
// reports whether it tagged anything.
func applyTag(item *yaml.Node, tag string) bool {
	applied := false
	for i := 0; i+1 < len(item.Content); i += 2 {
		key := unwrap(item.Content[i]).Value
		if !isMethod(key) {
			continue
		}
		op := unwrap(item.Content[i+1])
		if !isMap(op) || mapGet(op, "tags") != nil {
			continue
		}
		seq := &yaml.Node{
			Kind: yaml.SequenceNode, Tag: tagSeq, Style: yaml.FlowStyle,
		}
		seq.Content = append(seq.Content, scalar(tag))
		// Prepend, so the tag reads as a heading rather than trailing after
		// the responses block.
		op.Content = append([]*yaml.Node{scalar("tags"), seq}, op.Content...)
		applied = true
	}
	return applied
}

func tagNode(name, description string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	mapSet(n, "name", scalar(name))
	if description != "" {
		mapSet(n, "description", scalar(description))
	}
	return n
}

func infoNode(info UnifyInfo) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	mapSet(n, "title", scalar(info.Title))
	mapSet(n, "version", quotedScalar(info.Version))
	if info.Description != "" {
		mapSet(n, "description", scalar(info.Description))
	}
	return n
}

func serversNode(servers []Server) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq}
	for _, s := range servers {
		e := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
		mapSet(e, "url", scalar(s.URL))
		if s.Description != "" {
			mapSet(e, "description", scalar(s.Description))
		}
		n.Content = append(n.Content, e)
	}
	return n
}

// quotedScalar forces a string style, so a version such as 1.0 stays a string
// rather than being re-read as a float.
func quotedScalar(v string) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.ScalarNode, Tag: tagStr,
		Value: v, Style: yaml.DoubleQuotedStyle,
	}
}

// highestOpenAPI picks the greatest declared version, so stitching a 3.0 and a
// 3.1 document does not claim to be the older of the two.
func highestOpenAPI(sources []UnifySource) string {
	best := "3.1.0"
	for _, src := range sources {
		if src.Doc.OpenAPI > best {
			best = src.Doc.OpenAPI
		}
	}
	return best
}

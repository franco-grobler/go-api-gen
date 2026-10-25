package spec

import (
	"fmt"
	"reflect"
	"slices"

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

	schemes, err := b.mergeSecuritySchemes(opts.Sources)
	if err != nil {
		return nil, err
	}
	security, err := b.sharedSecurity(opts.Sources)
	if err != nil {
		return nil, err
	}

	paths, tags, err := b.mergePaths(opts.Sources, security == nil)
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
	if security != nil {
		mapSet(out, "security", security)
	}
	mapSet(out, "paths", paths)

	var comps *yaml.Node
	if len(opts.Components) > 0 {
		comps, err = b.components(BundleOptions{Components: opts.Components})
		if err != nil {
			return nil, err
		}
	}
	if schemes != nil {
		if comps == nil {
			comps = &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
		}
		mapSet(comps, "securitySchemes", schemes)
	}
	if comps != nil {
		mapSet(out, "components", comps)
	}
	return out, nil
}

// mergeSecuritySchemes combines every source's security schemes, or returns
// nil when none declares any.
//
// Services that share an issuer each declare the same scheme, so a name
// declared twice is kept once, with the first declaration's description, as
// long as both declarations agree. If they differ, one name would stand for
// two credentials in the unified document, and which one a requirement meant
// would depend on the service it came from.
func (b *bundler) mergeSecuritySchemes(
	sources []UnifySource,
) (*yaml.Node, error) {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	declarer := map[string]string{}
	for _, src := range sources {
		root, err := b.loader.File(src.Doc.File)
		if err != nil {
			return nil, err
		}
		schemes, err := b.securitySchemes(
			src.Doc.File,
			mapGet(root, "components"),
		)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.Doc.File, err)
		}
		if schemes == nil {
			continue
		}
		for i := 0; i+1 < len(schemes.Content); i += 2 {
			name := unwrap(schemes.Content[i]).Value
			scheme := schemes.Content[i+1]
			prev, dup := declarer[name]
			if !dup {
				declarer[name] = src.Doc.File
				mapSet(out, name, scheme)
				continue
			}
			same, err := sameScheme(mapGet(out, name), scheme)
			if err != nil {
				return nil, fmt.Errorf("%s: securityScheme %q: %w",
					src.Doc.File, name, err)
			}
			if !same {
				return nil, fmt.Errorf("securityScheme %q is declared "+
					"differently in %s and %s.\n"+
					"Make the two declarations identical, or give one of "+
					"them a distinct name, so the unified document has one "+
					"meaning for it.",
					name, prev, src.Doc.File)
			}
		}
	}
	if len(out.Content) == 0 {
		return nil, nil
	}
	return out, nil
}

// sharedSecurity returns the document-wide security requirements every source
// declares, when they all declare the same non-empty ones, or nil otherwise.
//
// The unified document has a single top level, so each service's own
// document-wide requirements cannot all live there. When they agree they are
// hoisted; when they do not, mergePaths copies each service's onto the
// operations that relied on it, so no operation loses its authentication by
// being merged.
func (b *bundler) sharedSecurity(sources []UnifySource) (*yaml.Node, error) {
	if len(sources) == 0 || len(sources[0].Doc.Security) == 0 {
		return nil, nil
	}
	first := sources[0].Doc
	for _, src := range sources[1:] {
		if !sameRequirements(first.Security, src.Doc.Security) {
			return nil, nil
		}
	}
	root, err := b.loader.File(first.File)
	if err != nil {
		return nil, err
	}
	sec, err := b.rewrite(first.File, mapGet(root, "security"))
	if err != nil {
		return nil, fmt.Errorf("%s: security: %w", first.File, err)
	}
	return sec, nil
}

// sameRequirements reports whether two security lists ask for the same
// schemes and scopes, in the same order.
func sameRequirements(a, b []Requirement) bool {
	sameRef := func(p, q SchemeRef) bool {
		return p.Name == q.Name && slices.Equal(p.Scopes, q.Scopes)
	}
	return slices.EqualFunc(a, b, func(x, y Requirement) bool {
		return slices.EqualFunc(x.Schemes, y.Schemes, sameRef)
	})
}

// sameScheme reports whether two security schemes describe the same
// credential. They are compared as decoded values, so formatting, comments,
// and key order do not count as a difference, and without their descriptions,
// since services that share an issuer each tend to word it their own way.
func sameScheme(a, b *yaml.Node) (bool, error) {
	av, err := anyVal(a)
	if err != nil {
		return false, err
	}
	bv, err := anyVal(b)
	if err != nil {
		return false, err
	}
	for _, v := range []any{av, bv} {
		if m, ok := v.(map[string]any); ok {
			delete(m, "description")
		}
	}
	return reflect.DeepEqual(av, bv), nil
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
//
// With inherit set, an operation that declares no security of its own is given
// its service's document-wide requirements, which the unified document's top
// level does not carry.
func (b *bundler) mergePaths(sources []UnifySource, inherit bool) (
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

		var security *yaml.Node
		if inherit && len(src.Doc.Security) > 0 {
			security, err = b.rewrite(src.Doc.File, mapGet(root, "security"))
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: security: %w",
					src.Doc.File,
					err,
				)
			}
		}

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
			if security != nil {
				applySecurity(item, security)
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

// applySecurity gives every operation of a path item that declares no security
// the given requirements. An operation that declares any, even an empty list,
// keeps its own.
func applySecurity(item, security *yaml.Node) {
	for i := 0; i+1 < len(item.Content); i += 2 {
		key := unwrap(item.Content[i]).Value
		if !isMethod(key) {
			continue
		}
		op := unwrap(item.Content[i+1])
		if !isMap(op) || mapGet(op, "security") != nil {
			continue
		}
		mapSet(op, "security", copyNode(security))
	}
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

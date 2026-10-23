package gen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/franco-grobler/go-api-gen/internal/config"
	"github.com/franco-grobler/go-api-gen/internal/gomap"
	"github.com/franco-grobler/go-api-gen/internal/naming"
	"github.com/franco-grobler/go-api-gen/internal/spec"
	"github.com/franco-grobler/go-api-gen/internal/workspace"
)

// Generator renders a workspace into files.
//
// Everything is generated into memory first, so a failure part way through
// leaves the tree untouched rather than half rewritten.
type Generator struct {
	ws     *workspace.Workspace
	mapper *gomap.Mapper
	files  map[string][]byte
}

// New returns a Generator for a resolved workspace.
func New(ws *workspace.Workspace) *Generator {
	return &Generator{
		ws:     ws,
		mapper: gomap.New(ws.Config),
		files:  map[string][]byte{},
	}
}

// Files returns the generated content keyed by absolute path.
func (g *Generator) Files() map[string][]byte { return g.files }

// Run generates every configured output.
func (g *Generator) Run() error {
	for _, root := range g.ws.Roots {
		if err := g.emitRoot(root); err != nil {
			return fmt.Errorf("shared root %s: %w", root.Name, err)
		}
	}
	for _, svc := range g.ws.Services {
		if err := g.emitService(svc); err != nil {
			return fmt.Errorf("service %s: %w", svc.Name, err)
		}
	}
	return nil
}

// emitRoot generates the one Go package that every service imports its shared
// schemas from.
func (g *Generator) emitRoot(root *workspace.Root) error {
	types := g.newTypeEmitter(nil, root.Schemas)

	file := NewFile(root.GoPackage, g.relSource(root.Cfg.From))
	if err := types.EmitTypes(file); err != nil {
		return err
	}
	path := filepath.Join(root.OutputDir, "types.gen.go")
	if err := g.add(path, file); err != nil {
		return err
	}

	// A shared root always emits validation: a service that imports one of
	// its types has no way to generate the missing method itself.
	valid := NewFile(root.GoPackage, g.relSource(root.Cfg.From))
	if err := g.EmitValidate(valid, nil, types.declared); err != nil {
		return err
	}
	return g.add(filepath.Join(root.OutputDir, "validate.gen.go"), valid)
}

func (g *Generator) emitService(svc *workspace.Service) error {
	types := g.newTypeEmitter(svc, svc.Local)

	// Naming is settled before anything is written. The server references
	// schemas the type walk never reaches on its own, such as an inline
	// response body, and those must be declared in types.gen.go rather than
	// referenced from a file that never declares them.
	if svc.Cfg.Generates(config.TargetServer) {
		if err := g.planOperations(svc, types); err != nil {
			return err
		}
	}

	if svc.Cfg.Generates(config.TargetTypes) {
		file := NewFile(svc.GoPackage, g.relSource(svc.Cfg.Spec))
		if err := types.EmitTypes(file); err != nil {
			return err
		}
		path := filepath.Join(svc.OutputDir, "types.gen.go")
		if err := g.add(path, file); err != nil {
			return err
		}
	}
	if svc.Cfg.Generates(config.TargetValidate) {
		file := NewFile(svc.GoPackage, g.relSource(svc.Cfg.Spec))
		if err := g.EmitValidate(file, svc, types.declared); err != nil {
			return err
		}
		path := filepath.Join(svc.OutputDir, "validate.gen.go")
		if err := g.add(path, file); err != nil {
			return err
		}
	}
	if svc.Cfg.Generates(config.TargetServer) {
		file := NewFile(svc.GoPackage, g.relSource(svc.Cfg.Spec))
		if err := g.EmitServer(file, svc, types); err != nil {
			return err
		}
		path := filepath.Join(svc.OutputDir, "server.gen.go")
		if err := g.add(path, file); err != nil {
			return err
		}
	}
	if svc.Cfg.Generates(config.TargetSpec) {
		if err := g.emitSpec(svc); err != nil {
			return err
		}
	}
	return nil
}

// planOperations assigns Go names to every schema an operation touches,
// without emitting anything.
//
// Names are resolved against a scratch file whose imports are discarded; the
// real files register their own imports when they emit. Walking in document
// order keeps a synthesized name stable regardless of which emitter runs
// first.
func (g *Generator) planOperations(
	svc *workspace.Service, types *typeEmitter,
) error {
	scratch := NewFile("scratch", "")
	visit := func(s *spec.Schema, owner, role string) error {
		if s == nil {
			return nil
		}
		_, err := types.typeOf(scratch, s, owner, role)
		return err
	}

	if err := reserveServerNames(svc, types); err != nil {
		return err
	}

	names := naming.NewDeduper()
	for _, pi := range svc.Doc.Paths {
		for _, op := range pi.Operations {
			name := names.Take(naming.Exported(op.OperationID))

			// The request and parameter structs are named before anything
			// inline in the operation, and after every schema, so a schema
			// keeps the name its author gave it and the struct is the one
			// that yields. createAssistant's request would otherwise be
			// declared a second time when the document also has a
			// CreateAssistantRequest schema, as OpenAI's does.
			group := map[string]string{}
			for _, in := range []string{"path", inQuery, "header"} {
				group[in] = name + naming.Exported(in) + "Params"
				if len(op.Params(in)) > 0 {
					group[in] = types.dedupe.Take(group[in])
				}
			}
			// The request struct is in every handler's signature, so a clash
			// falls back to another word before it falls back to a number.
			request := name + "Request"
			if types.dedupe.Taken(request) {
				request = name + "Input"
			}
			types.opTypes[op] = opTypes{
				request: types.dedupe.Take(request),
				path:    group["path"],
				query:   group[inQuery],
				header:  group["header"],
			}

			for _, p := range op.Parameters {
				owner, ok := group[p.In]
				if !ok {
					// A cookie parameter is not decoded, but an inline
					// schema on it is still a type to name.
					owner = name + naming.Exported(p.In) + "Params"
				}
				if err := visit(p.Schema, owner,
					naming.Exported(p.Name)); err != nil {
					return err
				}
			}
			if m := op.RequestBody.JSON(); m != nil {
				if err := visit(m.Schema, name, "Body"); err != nil {
					return err
				}
			}
			for _, r := range op.Success() {
				if m := r.JSON(); m != nil {
					if err := visit(m.Schema, name, "Response"); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// reserveServerNames claims the package-level identifiers every generated
// server declares, before any inline schema is named.
//
// Unlike a request struct, these are the server's API and cannot yield, so a
// schema already holding one is an error the author resolves with x-go-name.
func reserveServerNames(svc *workspace.Service, types *typeEmitter) error {
	reserved := []string{
		"Handler", "ServerOption", "WithMiddleware", "WithErrorOptions",
		"NewServer", "Register", "DefaultErrorOptions",
	}
	if usesSecurity(svc.Doc) {
		reserved = append(reserved, "Authenticator")
	}
	for _, id := range reserved {
		if !types.dedupe.Taken(id) {
			types.dedupe.Take(id)
			continue
		}
		for _, s := range svc.Local {
			if types.ws.GoName(s) == id {
				return fmt.Errorf("%s: schema %q is named %s in Go, which "+
					"the generated server declares.\n"+
					"Set x-go-name on the schema to rename it.",
					s.Loc, s.Loc.ComponentName(), id)
			}
		}
	}
	return nil
}

// usesSecurity reports whether any operation names a security scheme, which
// is when the server declares an Authenticator.
func usesSecurity(doc *spec.Document) bool {
	for _, pi := range doc.Paths {
		for _, op := range pi.Operations {
			for _, req := range op.Security {
				if len(req.Schemes) > 0 {
					return true
				}
			}
		}
	}
	return false
}

// emitSpec writes the self-contained, tree-shaken OpenAPI document.
func (g *Generator) emitSpec(svc *workspace.Service) error {
	comps, err := g.bundleComponents(svc)
	if err != nil {
		return err
	}
	node, err := g.ws.Loader.Bundle(
		svc.Doc,
		spec.BundleOptions{Components: comps},
	)
	if err != nil {
		return err
	}
	data, err := spec.MarshalBundle(node)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	out.WriteString("# Code generated by go-api-gen. DO NOT EDIT.\n")
	fmt.Fprintf(&out, "# Source: %s\n", g.relSource(svc.Cfg.Spec))
	out.Write(data)

	g.files[filepath.Join(svc.OutputDir, "openapi.yaml")] = out.Bytes()
	return nil
}

// bundleComponents names each reachable schema for the bundled document.
//
// The bundle mixes a service's own schemas with ones imported from shared
// roots, so a name unique within each Go package can still collide here. The
// Go name is preferred so the document and the code agree, and a collision is
// resolved by qualifying with the owning package.
func (g *Generator) bundleComponents(svc *workspace.Service) (
	[]spec.BundleComponent,
	error,
) {
	taken := map[string]*spec.Schema{}
	var out []spec.BundleComponent

	for _, s := range svc.Reachable {
		name := g.ws.GoName(s)
		owner := g.ws.Owner(s)
		if prev, dup := taken[name]; dup && prev != s {
			name = naming.Exported(owner.GoPackage()) + name
		}
		if prev, dup := taken[name]; dup && prev != s {
			return nil, fmt.Errorf("%s: cannot name component %q uniquely in "+
				"the bundled spec; set x-go-name on one of the colliding "+
				"schemas", s.Loc, name)
		}
		taken[name] = s
		out = append(out, spec.BundleComponent{Schema: s, Name: name})
	}
	return out, nil
}

// add renders a file and stores it, keyed by its absolute path. A file with no
// declarations is skipped: a service whose every type comes from a shared root
// legitimately has nothing of its own to declare.
func (g *Generator) add(path string, file *File) error {
	if file.Empty() {
		return nil
	}
	data, err := file.Bytes()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", filepath.Base(path), err, data)
	}
	g.files[path] = data
	return nil
}

// relSource renders a spec path relative to the config directory, so the
// header does not embed an absolute path that differs per machine.
func (g *Generator) relSource(p string) string {
	if filepath.IsAbs(p) {
		if rel, err := filepath.Rel(g.ws.Config.Dir, p); err == nil {
			return filepath.ToSlash(rel)
		}
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(p)
}

// Paths returns the generated file paths, sorted.
func (g *Generator) Paths() []string {
	out := make([]string, 0, len(g.files))
	for p := range g.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Write writes every generated file, creating directories as needed, and
// returns the paths whose contents actually changed.
func (g *Generator) Write() ([]string, error) {
	var changed []string
	for _, path := range g.Paths() {
		data := g.files[path]
		if same, err := unchanged(path, data); err != nil {
			return nil, err
		} else if same {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, err
		}
		changed = append(changed, path)
	}
	return changed, nil
}

// Check reports which generated files are missing or out of date, without
// writing anything. This is what CI runs to prove the committed output matches
// the specs.
func (g *Generator) Check() ([]string, error) {
	var stale []string
	for _, path := range g.Paths() {
		same, err := unchanged(path, g.files[path])
		if err != nil {
			return nil, err
		}
		if !same {
			stale = append(stale, path)
		}
	}
	return stale, nil
}

func unchanged(path string, want []byte) (bool, error) {
	got, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(got, want), nil
}

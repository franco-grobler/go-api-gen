// Package workspace resolves a configuration and a set of specs into the
// ownership model the emitters need: which Go package each schema belongs to,
// what it is called there, and which packages must import which.
package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/franco-grobler/go-api-gen/internal/config"
	"github.com/franco-grobler/go-api-gen/internal/gomod"
	"github.com/franco-grobler/go-api-gen/internal/naming"
	"github.com/franco-grobler/go-api-gen/internal/spec"
)

// Workspace is a loaded, ownership-resolved view of the whole configuration.
type Workspace struct {
	Config *config.Config
	Loader *spec.Loader

	Roots    []*Root
	Packages []*Package

	owners  map[*spec.Schema]*Owner
	goNames map[*spec.Schema]string
}

// Root is a shared schema root: one directory of component files that
// generates into exactly one Go package, no matter how many services use it.
type Root struct {
	Cfg        *config.SchemaRoot
	Files      []string       // absolute paths of the YAML files under the root
	Schemas    []*spec.Schema // every component declared under the root
	GoPackage  string
	ImportPath string
	OutputDir  string
}

// Package is one service spec and the Go package generated from it.
type Package struct {
	Cfg        *config.Package
	Doc        *spec.Document
	GoPackage  string
	ImportPath string
	OutputDir  string

	// Reachable is every named schema the operations touch, tree-shaken.
	Reachable []*spec.Schema
	// Local is the subset of Reachable this package owns and must generate.
	Local []*spec.Schema
	// Imports is the shared roots this package needs, in a stable order.
	Imports []*Root
}

// Owner records which Go package generates a schema.
type Owner struct {
	Root *Root    // non-nil for a shared schema
	Pkg  *Package // non-nil for a schema local to one service
}

// GoPackage is the package name the schema is generated into.
func (o *Owner) GoPackage() string {
	if o.Root != nil {
		return o.Root.GoPackage
	}
	return o.Pkg.GoPackage
}

// ImportPath is the import path of the package the schema is generated into.
func (o *Owner) ImportPath() string {
	if o.Root != nil {
		return o.Root.ImportPath
	}
	return o.Pkg.ImportPath
}

// Load resolves a configuration into a Workspace.
func Load(cfg *config.Config) (*Workspace, error) {
	w := &Workspace{
		Config:  cfg,
		Loader:  spec.NewLoader(),
		owners:  map[*spec.Schema]*Owner{},
		goNames: map[*spec.Schema]string{},
	}
	res := gomod.NewResolver()

	if err := w.loadRoots(res); err != nil {
		return nil, err
	}
	if err := w.loadPackages(res); err != nil {
		return nil, err
	}
	if err := w.assignOwners(); err != nil {
		return nil, err
	}
	if err := w.assignNames(); err != nil {
		return nil, err
	}
	w.resolveImports()
	return w, nil
}

func (w *Workspace) loadRoots(res *gomod.Resolver) error {
	for _, rc := range w.Config.Schemas {
		files, err := yamlFiles(rc.AbsPath)
		if err != nil {
			return fmt.Errorf("schema root %s: %w", rc.Path, err)
		}
		if len(files) == 0 {
			return fmt.Errorf(
				"schema root %s: no .yaml or .yml files found",
				rc.Path,
			)
		}

		r := &Root{
			Cfg: rc, Files: files,
			GoPackage: rc.Package, OutputDir: rc.AbsOutput,
		}
		if r.ImportPath, err = importPath(
			res,
			rc.Import,
			rc.AbsOutput,
		); err != nil {
			return fmt.Errorf("schema root %s: %w", rc.Path, err)
		}
		for _, f := range files {
			doc, err := w.Loader.Document(f)
			if err != nil {
				return err
			}
			r.Schemas = append(r.Schemas, doc.Components...)
		}
		w.Roots = append(w.Roots, r)
	}
	return nil
}

func (w *Workspace) loadPackages(res *gomod.Resolver) error {
	for _, pc := range w.Config.Packages {
		doc, err := w.Loader.Document(pc.AbsSpec)
		if err != nil {
			return err
		}
		p := &Package{
			Cfg: pc, Doc: doc,
			GoPackage: pc.Package, OutputDir: pc.AbsOutput,
		}
		if p.ImportPath, err = importPath(res, "", pc.AbsOutput); err != nil {
			return fmt.Errorf("package %s: %w", pc.Spec, err)
		}
		p.Reachable = spec.Reachable(doc)
		w.Packages = append(w.Packages, p)
	}
	return nil
}

// importPath prefers an explicit configuration value and otherwise infers the
// path from the enclosing go.mod.
func importPath(res *gomod.Resolver, configured, dir string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	p, err := res.ImportPath(dir)
	if err != nil {
		return "", fmt.Errorf("%w; set \"import\" explicitly to override", err)
	}
	return p, nil
}

// assignOwners maps every schema onto the package that generates it. A schema
// under a shared root belongs to that root; one declared in a service spec
// belongs to that service.
func (w *Workspace) assignOwners() error {
	// Index shared roots by the files they cover.
	fileRoot := map[string]*Root{}
	for _, r := range w.Roots {
		for _, f := range r.Files {
			if prev, dup := fileRoot[f]; dup {
				return fmt.Errorf("%s is covered by two schema roots, %s and "+
					"%s; schema roots must not overlap",
					f, prev.Cfg.Path, r.Cfg.Path)
			}
			fileRoot[f] = r
		}
		for _, s := range r.Schemas {
			w.owners[s] = &Owner{Root: r}
		}
	}

	specPkg := map[string]*Package{}
	for _, p := range w.Packages {
		specPkg[p.Cfg.AbsSpec] = p
	}

	for _, p := range w.Packages {
		for _, s := range p.Reachable {
			if _, ok := w.owners[s]; ok {
				continue
			}
			file := s.Loc.File
			if r, ok := fileRoot[file]; ok {
				// Declared under a root but not reached when the root was
				// scanned; this happens only if the root scan missed the file.
				w.owners[s] = &Owner{Root: r}
				r.Schemas = append(r.Schemas, s)
				continue
			}
			if file == p.Cfg.AbsSpec {
				w.owners[s] = &Owner{Pkg: p}
				continue
			}
			if other, ok := specPkg[file]; ok {
				return w.leakError(s, other, p)
			}
			return fmt.Errorf("%s: schema %q is declared in %s, which is not "+
				"covered by any schema root or package spec.\n"+
				"Add a schemas entry whose path contains that file.",
				s.Loc, s.Loc.ComponentName(), rel(w.Config.Dir, file))
		}
	}

	// Ownership is assigned across every package before it is validated: a
	// schema claimed by the first package processed is still a leak when a
	// later package reaches it.
	for _, p := range w.Packages {
		for _, s := range p.Reachable {
			if o := w.owners[s]; o.Pkg != nil && o.Pkg != p {
				return w.leakError(s, o.Pkg, p)
			}
		}
	}

	// Local is the owned subset of Reachable, preserving reachability order.
	for _, p := range w.Packages {
		for _, s := range p.Reachable {
			if o := w.owners[s]; o.Pkg == p {
				p.Local = append(p.Local, s)
			}
		}
	}
	return nil
}

// leakError reports a schema declared in one service's spec being reached by
// another. Honouring it would mean generating the same type into two packages,
// which is exactly the duplication this tool exists to prevent.
func (w *Workspace) leakError(s *spec.Schema, owner, user *Package) error {
	return fmt.Errorf("%s: schema %q is declared in %s, the spec for package "+
		"%q, but is referenced by package %q.\n"+
		"Move it into a shared schema root so both packages can import one Go "+
		"type.",
		s.Loc, s.Loc.ComponentName(), rel(
			w.Config.Dir,
			s.Loc.File,
		), owner.Cfg.Output, user.Cfg.Output)
}

// assignNames gives every schema its Go type name and rejects collisions
// within a package. Silently renaming a shared type would make its Go name
// depend on load order, so a collision is an error the author resolves.
func (w *Workspace) assignNames() error {
	type claim struct {
		schema *spec.Schema
		name   string
	}
	claimed := map[string]map[string]claim{} // package key -> Go name -> claim

	assign := func(s *spec.Schema, o *Owner, prefix string) error {
		name := s.GoName
		if name == "" {
			name = naming.Exported(prefix + s.Loc.ComponentName())
		}
		key := o.ImportPath()
		if claimed[key] == nil {
			claimed[key] = map[string]claim{}
		}
		if prev, dup := claimed[key][name]; dup && prev.schema != s {
			return fmt.Errorf("Go type name %q is claimed twice in package "+
				"%q:\n  %s\n  %s\n"+
				"Set x-go-name on one of them, or give the schema root a "+
				"name_prefix.",
				name, o.GoPackage(), prev.schema.Loc, s.Loc)
		}
		claimed[key][name] = claim{s, name}
		w.goNames[s] = name
		return nil
	}

	for _, r := range w.Roots {
		for _, s := range r.Schemas {
			if err := assign(s, &Owner{Root: r}, r.Cfg.NamePrefix); err != nil {
				return err
			}
		}
	}
	for _, p := range w.Packages {
		for _, s := range p.Local {
			if err := assign(s, &Owner{Pkg: p}, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveImports records, per package, which shared roots it must import.
func (w *Workspace) resolveImports() {
	for _, p := range w.Packages {
		seen := map[*Root]bool{}
		for _, s := range p.Reachable {
			o := w.owners[s]
			if o.Root != nil && !seen[o.Root] {
				seen[o.Root] = true
				p.Imports = append(p.Imports, o.Root)
			}
		}
	}
}

// Owner returns the package that generates a schema, or nil for an inline one.
func (w *Workspace) Owner(s *spec.Schema) *Owner { return w.owners[s] }

// GoName returns the Go type name of a named schema.
func (w *Workspace) GoName(s *spec.Schema) string { return w.goNames[s] }

// Qualify returns how a schema is referred to from within a given package:
// bare when it is local, package-qualified when it is imported.
func (w *Workspace) Qualify(s *spec.Schema, from *Package) string {
	name := w.goNames[s]
	o := w.owners[s]
	if o == nil || name == "" {
		return name
	}
	if o.Pkg == from {
		return name
	}
	return o.GoPackage() + "." + name
}

// yamlFiles lists the YAML documents under a root, sorted, skipping hidden
// directories so that stray editor or VCS state never becomes a schema.
func yamlFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(
		path string,
		d fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if ext := filepath.Ext(name); ext == ".yaml" || ext == ".yml" {
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			out = append(out, abs)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func rel(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil {
		return r
	}
	return path
}

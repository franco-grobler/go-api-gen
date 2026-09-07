// Package config loads and validates the go-api-gen workspace configuration.
//
// A workspace has one config file, conventionally go-api-gen.yaml at the
// repository root beside go.mod, so that every path in it is repository
// relative rather than a chain of "..". It declares shared schema roots, which
// each become a single Go package generated once, and services, whose specs
// reference those roots.
//
// Shared roots and services are keyed by name rather than listed, so that a
// diagnostic can say "services.billing" instead of "packages[2]", and
// unified.exclude can name a service instead of repeating its output path.
//
// Loading goes through Viper, so the file may be written as YAML, JSON, or
// TOML, and a handful of settings can be overridden from the environment with
// the GOAPIGEN_ prefix, which is what CI needs when a base URL differs per
// environment. Viper lower-cases every key it reads, which is why names are
// compared case-insensitively throughout.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// EnvPrefix is the environment variable prefix for configuration overrides.
const EnvPrefix = "GOAPIGEN"

// ConfigName is the base name searched for when discovering a config file.
const ConfigName = "go-api-gen"

// SupportedExts are the config file extensions Viper will read, in the order
// it prefers them.
var SupportedExts = []string{"yaml", "yml", "json", "toml"}

// Target names a kind of generated output.
type Target string

const (
	TargetTypes    Target = "types"
	TargetValidate Target = "validate"
	TargetServer   Target = "server"
	TargetClient   Target = "client"
	TargetFake     Target = "fake"
	TargetSpec     Target = "spec"
)

// AllTargets is every target the configuration accepts, in emission order.
var AllTargets = []Target{
	TargetTypes, TargetValidate, TargetServer,
	TargetClient, TargetFake, TargetSpec,
}

// targetRequires records what a target cannot be generated without: validation
// is emitted into the types file's package, and the server decodes into
// generated types. Naming a target pulls its prerequisites in rather than
// failing, because there is exactly one thing the author can have meant.
var targetRequires = map[Target][]Target{
	TargetValidate: {TargetTypes},
	TargetServer:   {TargetTypes},
	TargetClient:   {TargetTypes},
	TargetFake:     {TargetServer, TargetTypes},
}

// unimplementedTargets are accepted by the schema but not yet emitted. They
// are excluded from the default set, so that a workspace which does not ask
// for one does not silently write nothing. Remove an entry as its emitter
// lands.
var unimplementedTargets = map[Target]string{}

// DefaultTargets is what a workspace generates when it does not say.
func DefaultTargets() []Target {
	out := make([]Target, 0, len(AllTargets))
	for _, t := range AllTargets {
		if _, pending := unimplementedTargets[t]; !pending {
			out = append(out, t)
		}
	}
	return out
}

func knownTarget(t Target) bool {
	for _, k := range AllTargets {
		if k == t {
			return true
		}
	}
	return false
}

// expandTargets returns ts with every prerequisite pulled in, ordered by
// AllTargets so that emission order does not depend on how the list was
// written. Unrecognised entries are kept, deduplicated, at the end, so that
// validation can report them against the name the author actually used.
func expandTargets(ts []Target) []Target {
	want := map[Target]bool{}
	seen := map[Target]bool{}
	var unknown []Target
	for _, t := range ts {
		if !knownTarget(t) {
			if !seen[t] {
				seen[t] = true
				unknown = append(unknown, t)
			}
			continue
		}
		want[t] = true
		for _, req := range targetRequires[t] {
			want[req] = true
		}
	}
	out := make([]Target, 0, len(want)+len(unknown))
	for _, t := range AllTargets {
		if want[t] {
			out = append(out, t)
		}
	}
	return append(out, unknown...)
}

// Config is a parsed and validated workspace configuration.
type Config struct {
	Version int `mapstructure:"version"`

	// Shared and Services are keyed by name. Iterate them through SharedRoots
	// and ServiceList rather than directly: map order is random, and generated
	// output must not be.
	Shared   map[string]*SchemaRoot `mapstructure:"shared"`
	Services map[string]*Service    `mapstructure:"services"`

	// Generate is the default target list, which a service may override with
	// its own.
	Generate []Target `mapstructure:"generate"`

	Types   map[string]TypeMapping `mapstructure:"types"`
	Errors  ErrorsConfig           `mapstructure:"errors"`
	Unified *UnifiedConfig         `mapstructure:"unified"`

	// Path is the absolute path of the config file, and Dir its directory.
	// Every relative path in the file is resolved against Dir.
	Path string `mapstructure:"-"`
	Dir  string `mapstructure:"-"`

	sharedRoots []*SchemaRoot
	services    []*Service
}

// SharedRoots returns the shared schema roots in name order.
func (c *Config) SharedRoots() []*SchemaRoot { return c.sharedRoots }

// ServiceList returns the services in name order.
func (c *Config) ServiceList() []*Service { return c.services }

// SchemaRoot is a directory of shared component files that generates into one
// Go package. Every schema defined under it is owned by that package, and a
// service that $refs one imports it rather than regenerating its own copy.
type SchemaRoot struct {
	// Name is the key the root was declared under. It identifies the root in
	// diagnostics.
	Name string `mapstructure:"-"`

	From    string `mapstructure:"from"`
	To      string `mapstructure:"to"`
	Package string `mapstructure:"package"`
	Import  string `mapstructure:"import"`

	// Prefix disambiguates Go type names when two roots define the same
	// component name. It is applied to every type generated from this root.
	Prefix string `mapstructure:"prefix"`

	// AbsFrom and AbsTo are From and To resolved against Config.Dir.
	AbsFrom string `mapstructure:"-"`
	AbsTo   string `mapstructure:"-"`
}

// Service is one service spec and the Go package generated from it.
type Service struct {
	// Name is the key the service was declared under. It identifies the
	// service in diagnostics, in unified.exclude, and in the unified
	// document's tags.
	Name string `mapstructure:"-"`

	Spec     string   `mapstructure:"spec"`
	To       string   `mapstructure:"to"`
	Package  string   `mapstructure:"package"`
	Generate []Target `mapstructure:"generate"`

	// AbsSpec and AbsTo are Spec and To resolved against Config.Dir.
	AbsSpec string `mapstructure:"-"`
	AbsTo   string `mapstructure:"-"`

	// ownTargets records that the service declared its own target list, so an
	// unknown target is reported once, against the place it was written,
	// rather than once per service that inherited the workspace default.
	ownTargets bool
}

// Generates reports whether this service emits the given target.
func (s *Service) Generates(t Target) bool {
	for _, g := range s.Generate {
		if g == t {
			return true
		}
	}
	return false
}

// TypeMapping maps an OpenAPI format or a named schema onto a Go type.
type TypeMapping struct {
	Go     string `mapstructure:"go"`
	Import string `mapstructure:"import"`
}

// formatKey namespaces format mappings inside the one "types" map. An OpenAPI
// component name cannot contain a colon, so the two namespaces cannot collide.
const formatKey = "format:"

// ErrorsConfig controls the generated problem+json envelope.
type ErrorsConfig struct {
	// TypeBaseURL is prefixed to an error slug to form the RFC 9457 "type" URI.
	TypeBaseURL string `mapstructure:"type_base_url"`
	// ValidationTitle overrides the default title for validation failures.
	ValidationTitle string `mapstructure:"validation_title"`
}

// UnifiedConfig describes the single stitched-together document produced by
// the unify command. It is a separate deliverable from the per-service
// bundles: those each describe one service, while this describes the platform
// as a whole, for a gateway or a docs portal.
type UnifiedConfig struct {
	To          string   `mapstructure:"to"`
	Title       string   `mapstructure:"title"`
	Version     string   `mapstructure:"version"`
	Description string   `mapstructure:"description"`
	Servers     []Server `mapstructure:"servers"`

	// TagWithService tags each operation that declares no tags of its own with
	// its service's title, so a merged document stays navigable. It defaults
	// to true; set it false to leave operations untagged.
	TagWithService *bool `mapstructure:"tag_with_service"`

	// Exclude names services to leave out, for internal ones that should not
	// appear in a platform-wide document.
	Exclude []string `mapstructure:"exclude"`

	// AbsTo is To resolved against Config.Dir.
	AbsTo string `mapstructure:"-"`
}

// Server is one entry of a servers list.
type Server struct {
	URL         string `mapstructure:"url"`
	Description string `mapstructure:"description"`
}

// Tags reports whether operations should be tagged with their service.
func (u *UnifiedConfig) Tags() bool {
	return u.TagWithService == nil || *u.TagWithService
}

// Excludes reports whether a service is left out of the unified document.
// The comparison is case-insensitive because Viper lower-cases the names it
// reads as map keys but leaves the strings in this list alone.
func (u *UnifiedConfig) Excludes(service string) bool {
	for _, e := range u.Exclude {
		if strings.EqualFold(e, service) {
			return true
		}
	}
	return false
}

// TypeFor returns the Go type override configured for a named schema.
// Lookup is case-insensitive because Viper lower-cases configuration keys.
func (c *Config) TypeFor(schemaName string) (TypeMapping, bool) {
	m, ok := c.Types[strings.ToLower(schemaName)]
	return m, ok
}

// FormatFor returns the Go type configured for an OpenAPI format.
// Lookup is case-insensitive because Viper lower-cases configuration keys.
func (c *Config) FormatFor(format string) (TypeMapping, bool) {
	m, ok := c.Types[formatKey+strings.ToLower(format)]
	return m, ok
}

// HasService reports whether a service of this name is configured.
func (c *Config) HasService(name string) bool {
	for _, s := range c.services {
		if strings.EqualFold(s.Name, name) {
			return true
		}
	}
	return false
}

// envOverrides are the settings that may be overridden from the environment.
// Binding is explicit rather than automatic so that a stray GOAPIGEN_ variable
// can never inject a key that strict decoding would then reject.
var envOverrides = []string{
	"errors.type_base_url",
	"errors.validation_title",
}

// newViper returns a Viper configured the way every entry point expects.
func newViper() (*viper.Viper, error) {
	v := viper.NewWithOptions(viper.KeyDelimiter("."))
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.SetDefault("version", 1)
	for _, key := range envOverrides {
		if err := v.BindEnv(key); err != nil {
			return nil, fmt.Errorf("bind %s: %w", key, err)
		}
	}
	return v, nil
}

// Load reads, defaults, and validates the config file at path.
func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	v, err := newViper()
	if err != nil {
		return nil, err
	}
	v.SetConfigFile(abs)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", abs, err)
	}
	return fromViper(v, abs)
}

// Parse decodes config bytes that came from the file at abs. The file
// extension of abs selects the format.
func Parse(data []byte, abs string) (*Config, error) {
	ext := strings.TrimPrefix(filepath.Ext(abs), ".")
	if ext == "" {
		ext = "yaml"
	}
	v, err := newViper()
	if err != nil {
		return nil, err
	}
	v.SetConfigType(ext)
	if err := v.ReadConfig(strings.NewReader(string(data))); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", filepath.Base(abs), err)
	}
	return fromViper(v, abs)
}

// Discover walks up from dir looking for a config file, then loads it.
func Discover(dir string) (*Config, error) {
	path, err := FindConfig(dir)
	if err != nil {
		return nil, err
	}
	return Load(path)
}

// fromViper decodes an already-populated Viper into a validated Config.
func fromViper(v *viper.Viper, abs string) (*Config, error) {
	var c Config
	err := v.Unmarshal(&c, func(dc *mapstructure.DecoderConfig) {
		// Reject unknown keys so a typo is a failed run rather than a setting
		// that silently does nothing.
		dc.ErrorUnused = true
		dc.ZeroFields = true
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToSliceHookFunc(","),
			mapstructure.TextUnmarshallerHookFunc(),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", abs, err)
	}

	c.Path = abs
	c.Dir = filepath.Dir(abs)
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// FindConfig walks up from dir looking for a config file in any supported
// format. Viper's own search paths do not walk parents, so the walk is here.
func FindConfig(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		for _, ext := range SupportedExts {
			p := filepath.Join(abs, ConfigName+"."+ext)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, nil
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no %s.{%s} found in %s or any parent "+
				"directory",
				ConfigName, strings.Join(SupportedExts, ","), dir)
		}
		abs = parent
	}
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Generate == nil {
		c.Generate = DefaultTargets()
	}
	c.Generate = expandTargets(c.Generate)

	c.sharedRoots = make([]*SchemaRoot, 0, len(c.Shared))
	for name, r := range c.Shared {
		if r == nil {
			r = &SchemaRoot{}
			c.Shared[name] = r
		}
		r.Name = name
		r.AbsFrom = c.resolve(r.From)
		r.AbsTo = c.resolve(r.To)
		if r.Package == "" {
			r.Package = goPackageName(r.AbsTo)
		}
		c.sharedRoots = append(c.sharedRoots, r)
	}
	sort.Slice(c.sharedRoots, func(i, j int) bool {
		return c.sharedRoots[i].Name < c.sharedRoots[j].Name
	})

	c.services = make([]*Service, 0, len(c.Services))
	for name, s := range c.Services {
		if s == nil {
			s = &Service{}
			c.Services[name] = s
		}
		s.Name = name
		s.AbsSpec = c.resolve(s.Spec)
		s.AbsTo = c.resolve(s.To)
		if s.Package == "" {
			s.Package = goPackageName(s.AbsTo)
		}
		s.ownTargets = s.Generate != nil
		if s.Generate == nil {
			s.Generate = c.Generate
		}
		s.Generate = expandTargets(s.Generate)
		c.services = append(c.services, s)
	}
	sort.Slice(c.services, func(i, j int) bool {
		return c.services[i].Name < c.services[j].Name
	})

	if u := c.Unified; u != nil {
		u.AbsTo = c.resolve(u.To)
		if u.Title == "" {
			u.Title = "Unified API"
		}
		if u.Version == "" {
			u.Version = "0.0.0"
		}
	}
}

func (c *Config) resolve(p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(c.Dir, p))
}

// goPackageName derives an identifier from an output directory basename.
func goPackageName(dir string) string {
	base := filepath.Base(dir)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	s := b.String()
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		s = "api" + s
	}
	return s
}

func (c *Config) validate() error {
	var errs []string
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	if c.Version != 1 {
		add("version: unsupported version %d (expected 1)", c.Version)
	}
	if len(c.Shared) == 0 && len(c.Services) == 0 {
		add("config declares neither shared schema roots nor services")
	}

	checkTargets := func(where string, ts []Target) {
		for _, t := range ts {
			if !knownTarget(t) {
				add(
					"%s: unknown target %q (known: %s)",
					where,
					t,
					targetList(),
				)
			}
		}
	}
	checkTargets("generate", c.Generate)

	// Output directories must be unique: two generators writing the same
	// directory would silently clobber each other's files.
	owner := map[string]string{}
	claim := func(dir, by string) {
		if dir == "" {
			return
		}
		if prev, ok := owner[dir]; ok {
			add("output %q is claimed by both %s and %s", dir, prev, by)
			return
		}
		owner[dir] = by
	}

	seenFrom := map[string]string{}
	for _, r := range c.sharedRoots {
		where := "shared." + r.Name
		if r.Name == "" {
			add("shared: an entry is declared under an empty name")
		}
		if r.From == "" {
			add("%s: from is required", where)
		} else {
			if prev, dup := seenFrom[r.AbsFrom]; dup {
				add("%s: from %q is already used by %s", where, r.From, prev)
			} else {
				seenFrom[r.AbsFrom] = where
			}
			if fi, err := os.Stat(r.AbsFrom); err != nil {
				add("%s: from %q: %v", where, r.From, err)
			} else if !fi.IsDir() {
				add("%s: from %q is not a directory", where, r.From)
			}
		}
		if r.To == "" {
			add("%s: to is required", where)
		}
		if err := validIdent(r.Package); err != nil {
			add("%s: package: %v", where, err)
		}
		claim(r.AbsTo, where)
	}

	seenSpec := map[string]string{}
	for _, s := range c.services {
		where := "services." + s.Name
		if s.Name == "" {
			add("services: an entry is declared under an empty name")
		}
		if s.Spec == "" {
			add("%s: spec is required", where)
		} else {
			if prev, dup := seenSpec[s.AbsSpec]; dup {
				add("%s: spec %q is already used by %s", where, s.Spec, prev)
			} else {
				seenSpec[s.AbsSpec] = where
			}
			if _, err := os.Stat(s.AbsSpec); err != nil {
				add("%s: spec %q: %v", where, s.Spec, err)
			}
		}
		if s.To == "" {
			add("%s: to is required", where)
		}
		if err := validIdent(s.Package); err != nil {
			add("%s: package: %v", where, err)
		}
		if s.ownTargets {
			checkTargets(where+".generate", s.Generate)
		}
		claim(s.AbsTo, where)
	}

	if u := c.Unified; u != nil {
		for _, e := range u.Exclude {
			if !c.HasService(e) {
				add("unified.exclude: %q does not name a service", e)
			}
		}
		for i, s := range u.Servers {
			if s.URL == "" {
				add("unified.servers[%d]: url is required", i)
			}
		}
		claim(u.AbsTo, "unified")
	}

	for name, m := range c.Types {
		if m.Go == "" {
			add("types[%q]: go is required", name)
			continue
		}
		// A qualified type with no import would generate code that does not
		// compile, and the failure would surface far from its cause.
		if qualifier, _, ok := strings.Cut(
			m.Go,
			".",
		); ok && m.Import == "" && !strings.HasPrefix(m.Go, "[]") {
			add("types[%q]: go %q is qualified by package %q but no import is "+
				"set", name, m.Go, qualifier)
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf(
			"invalid config %s:\n  - %s",
			c.Path,
			strings.Join(errs, "\n  - "),
		)
	}
	return nil
}

func targetList() string {
	parts := make([]string, len(AllTargets))
	for i, t := range AllTargets {
		parts[i] = string(t)
	}
	return strings.Join(parts, ", ")
}

func validIdent(s string) error {
	if s == "" {
		return fmt.Errorf("is empty and could not be derived from the output " +
			"directory")
	}
	for i, r := range s {
		ok := r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("%q is not a valid Go identifier", s)
		}
	}
	return nil
}

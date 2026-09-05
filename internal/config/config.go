// Package config loads and validates the go-api-gen workspace configuration.
//
// A workspace has one config file (conventionally api/go-api-gen.yaml). It
// declares shared schema roots, which each become a single Go package generated
// once, and packages, which are per-service specs that reference those roots.
//
// Loading goes through Viper, so the file may be written as YAML, JSON, or
// TOML, and a handful of settings can be overridden from the environment with
// the GOAPIGEN_ prefix, which is what CI needs when a base URL differs per
// environment. Viper lower-cases every key it reads, so the map keys under
// "formats" and "types" are matched case-insensitively; see TypeFor.
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

// unimplementedTargets are accepted by the schema but not yet emitted.
//
// They are excluded from the default set and rejected when named explicitly,
// so that asking for one fails loudly instead of producing a successful run
// that quietly writes nothing. Remove an entry as its emitter lands.
var unimplementedTargets = map[Target]string{}

// DefaultTargets is what a package generates when it does not say.
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

// Config is a parsed and validated workspace configuration.
type Config struct {
	Version  int                    `mapstructure:"version"`
	Schemas  []*SchemaRoot          `mapstructure:"schemas"`
	Formats  map[string]TypeMapping `mapstructure:"formats"`
	Types    map[string]TypeMapping `mapstructure:"types"`
	Errors   ErrorsConfig           `mapstructure:"errors"`
	Packages []*Package             `mapstructure:"packages"`
	Unified  *UnifiedConfig         `mapstructure:"unified"`

	// Path is the absolute path of the config file, and Dir its directory.
	// Every relative path in the file is resolved against Dir.
	Path string `mapstructure:"-"`
	Dir  string `mapstructure:"-"`
}

// SchemaRoot is a directory of shared component files that generate into one
// Go package. Every schema defined under it is owned by that package, and
// service specs that $ref it import rather than regenerate it.
type SchemaRoot struct {
	Path    string `mapstructure:"path"`
	Output  string `mapstructure:"output"`
	Package string `mapstructure:"package"`
	Import  string `mapstructure:"import"`

	// NamePrefix disambiguates Go type names when two roots define the same
	// component name. It is applied to every type generated from this root.
	NamePrefix string `mapstructure:"name_prefix"`

	// AbsPath and AbsOutput are Path and Output resolved against Config.Dir.
	AbsPath   string `mapstructure:"-"`
	AbsOutput string `mapstructure:"-"`
}

// TypeMapping maps an OpenAPI format (or a named schema) onto a Go type.
type TypeMapping struct {
	Type   string `mapstructure:"type"`
	Import string `mapstructure:"import"`
}

// ErrorsConfig controls the generated problem+json envelope.
type ErrorsConfig struct {
	// TypeBaseURL is prefixed to an error slug to form the RFC 9457 "type" URI.
	TypeBaseURL string `mapstructure:"type_base_url"`
	// ValidationTitle overrides the default title for validation failures.
	ValidationTitle string `mapstructure:"validation_title"`
}

// UnifiedConfig describes the single stitched-together document produced by
// the unify command. It is a separate deliverable from the per-package
// bundles: those each describe one service, while this describes the platform
// as a whole, for a gateway or a docs portal.
type UnifiedConfig struct {
	Output      string   `mapstructure:"output"`
	Title       string   `mapstructure:"title"`
	Version     string   `mapstructure:"version"`
	Description string   `mapstructure:"description"`
	Servers     []Server `mapstructure:"servers"`

	// TagWithPackage tags each operation that declares no tags of its own with
	// the contributing package's name, so a merged document stays navigable.
	// It defaults to true; set it false to leave operations untagged.
	TagWithPackage *bool `mapstructure:"tag_with_package"`

	// Exclude lists package output paths to leave out, for internal services
	// that should not appear in a platform-wide document.
	Exclude []string `mapstructure:"exclude"`

	AbsOutput string `mapstructure:"-"`
}

// Server is one entry of a servers list.
type Server struct {
	URL         string `mapstructure:"url"`
	Description string `mapstructure:"description"`
}

// Tags reports whether operations should be tagged with their package name.
func (u *UnifiedConfig) Tags() bool {
	return u.TagWithPackage == nil || *u.TagWithPackage
}

// Excludes reports whether a package output path is excluded from the unified
// document.
func (u *UnifiedConfig) Excludes(output string) bool {
	for _, e := range u.Exclude {
		if e == output {
			return true
		}
	}
	return false
}

// Package is one service spec and the Go package generated from it.
type Package struct {
	Spec     string   `mapstructure:"spec"`
	Output   string   `mapstructure:"output"`
	Package  string   `mapstructure:"package"`
	Generate []Target `mapstructure:"generate"`

	AbsSpec   string `mapstructure:"-"`
	AbsOutput string `mapstructure:"-"`
}

// Generates reports whether this package emits the given target.
func (p *Package) Generates(t Target) bool {
	for _, g := range p.Generate {
		if g == t {
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
	m, ok := c.Formats[strings.ToLower(format)]
	return m, ok
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
	for _, s := range c.Schemas {
		s.AbsPath = c.resolve(s.Path)
		s.AbsOutput = c.resolve(s.Output)
		if s.Package == "" {
			s.Package = goPackageName(s.AbsOutput)
		}
	}
	for _, p := range c.Packages {
		p.AbsSpec = c.resolve(p.Spec)
		p.AbsOutput = c.resolve(p.Output)
		if p.Package == "" {
			p.Package = goPackageName(p.AbsOutput)
		}
		if p.Generate == nil {
			p.Generate = DefaultTargets()
		}
	}
	if u := c.Unified; u != nil {
		u.AbsOutput = c.resolve(u.Output)
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
	if len(c.Schemas) == 0 && len(c.Packages) == 0 {
		add("config declares neither schemas nor packages")
	}

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

	seenRoot := map[string]bool{}
	for i, s := range c.Schemas {
		where := fmt.Sprintf("schemas[%d]", i)
		if s.Path == "" {
			add("%s: path is required", where)
		} else if seenRoot[s.AbsPath] {
			add("%s: duplicate schema root path %q", where, s.Path)
		} else {
			seenRoot[s.AbsPath] = true
			if fi, err := os.Stat(s.AbsPath); err != nil {
				add("%s: path %q: %v", where, s.Path, err)
			} else if !fi.IsDir() {
				add("%s: path %q is not a directory", where, s.Path)
			}
		}
		if s.Output == "" {
			add("%s: output is required", where)
		}
		if err := validIdent(s.Package); err != nil {
			add("%s: package: %v", where, err)
		}
		claim(s.AbsOutput, where)
	}

	seenSpec := map[string]bool{}
	for i, p := range c.Packages {
		where := fmt.Sprintf("packages[%d]", i)
		if p.Spec == "" {
			add("%s: spec is required", where)
		} else if seenSpec[p.AbsSpec] {
			add("%s: duplicate spec %q", where, p.Spec)
		} else {
			seenSpec[p.AbsSpec] = true
			if _, err := os.Stat(p.AbsSpec); err != nil {
				add("%s: spec %q: %v", where, p.Spec, err)
			}
		}
		if p.Output == "" {
			add("%s: output is required", where)
		}
		if err := validIdent(p.Package); err != nil {
			add("%s: package: %v", where, err)
		}
		for _, t := range p.Generate {
			if !knownTarget(t) {
				add(
					"%s: generate: unknown target %q (known: %s)",
					where,
					t,
					targetList(),
				)
			}
		}
		// Validation code is emitted into the types file's package and is
		// meaningless without it; the server decodes into generated types.
		if p.Generates(TargetValidate) && !p.Generates(TargetTypes) {
			add("%s: generate: \"validate\" requires \"types\"", where)
		}
		if p.Generates(TargetServer) && !p.Generates(TargetTypes) {
			add("%s: generate: \"server\" requires \"types\"", where)
		}
		if p.Generates(TargetClient) && !p.Generates(TargetTypes) {
			add("%s: generate: \"client\" requires \"types\"", where)
		}
		if p.Generates(TargetFake) && !p.Generates(TargetServer) {
			add("%s: generate: \"fake\" requires \"server\"", where)
		}
		claim(p.AbsOutput, where)
	}

	checkMapping := func(section, name string, m TypeMapping) {
		if m.Type == "" {
			add("%s[%q]: type is required", section, name)
			return
		}
		// A qualified type with no import would generate code that does not
		// compile, and the failure would surface far from its cause.
		if qualifier, _, ok := strings.Cut(
			m.Type,
			".",
		); ok && m.Import == "" && !strings.HasPrefix(m.Type, "[]") {
			add("%s[%q]: type %q is qualified by package %q but no import is "+
				"set", section, name, m.Type, qualifier)
		}
	}
	if u := c.Unified; u != nil {
		outputs := map[string]bool{}
		for _, p := range c.Packages {
			outputs[p.Output] = true
		}
		for _, e := range u.Exclude {
			if !outputs[e] {
				add("unified.exclude: %q does not match any package output", e)
			}
		}
		for i, s := range u.Servers {
			if s.URL == "" {
				add("unified.servers[%d]: url is required", i)
			}
		}
		claim(u.AbsOutput, "unified")
	}
	for name, m := range c.Formats {
		checkMapping("formats", name, m)
	}
	for name, m := range c.Types {
		checkMapping("types", name, m)
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

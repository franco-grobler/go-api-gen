// Package gomap maps OpenAPI scalar types and formats onto Go types.
//
// The defaults follow what a Go author would write by hand; the workspace
// config overrides any of them, which is how a team plugs in uuid.UUID or
// decimal.Decimal without patching the generator.
package gomap

import (
	"github.com/franco-grobler/go-api-gen/internal/config"
	"github.com/franco-grobler/go-api-gen/internal/spec"
)

// Type is a Go type expression together with the import it needs.
type Type struct {
	Expr   string // for example "string", "time.Time", "uuid.UUID"
	Import string // import path, empty for builtins
}

// Mapper resolves scalars using the workspace configuration.
type Mapper struct {
	cfg *config.Config
}

// New returns a Mapper backed by cfg.
func New(cfg *config.Config) *Mapper { return &Mapper{cfg: cfg} }

// defaults maps a JSON Schema type and format to a Go type. An empty format
// key is the fallback for that type.
var defaults = map[string]map[string]Type{
	"string": {
		"":          {Expr: "string"},
		"date-time": {Expr: "time.Time", Import: "time"},
		"date":      {Expr: "time.Time", Import: "time"},
		"byte":      {Expr: "[]byte"},
		"binary":    {Expr: "[]byte"},
	},
	"integer": {
		"":      {Expr: "int"},
		"int32": {Expr: "int32"},
		"int64": {Expr: "int64"},
	},
	"number": {
		"":       {Expr: "float64"},
		"float":  {Expr: "float32"},
		"double": {Expr: "float64"},
	},
	"boolean": {
		"": {Expr: "bool"},
	},
}

// Scalar returns the Go type for a schema whose type is a JSON scalar, and
// reports whether the schema is in fact a scalar.
//
// Resolution order is: an explicit x-go-type on the schema, then a configured
// format mapping, then the built-in defaults. A format nobody has mapped falls
// back to the base type rather than failing, since most formats are
// annotations a Go type cannot express anyway.
func (m *Mapper) Scalar(s *spec.Schema) (Type, bool) {
	if s == nil {
		return Type{}, false
	}
	if s.GoType != "" {
		return Type{Expr: s.GoType, Import: s.GoImport}, true
	}

	base := s.SingleType()
	byFormat, ok := defaults[base]
	if !ok {
		return Type{}, false
	}
	if s.Format != "" {
		if mapping, ok := m.cfg.FormatFor(s.Format); ok {
			return Type{Expr: mapping.Type, Import: mapping.Import}, true
		}
		if t, ok := byFormat[s.Format]; ok {
			return t, true
		}
	}
	return byFormat[""], true
}

// Configured returns a Go type override configured for a named schema.
func (m *Mapper) Configured(schemaName string) (Type, bool) {
	mapping, ok := m.cfg.TypeFor(schemaName)
	if !ok {
		return Type{}, false
	}
	return Type{Expr: mapping.Type, Import: mapping.Import}, true
}

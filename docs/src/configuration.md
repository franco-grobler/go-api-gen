# Configuration

One file, at the repo root beside `go.mod`, so no path in it needs a `../`:

```yaml
version: 1

shared:
  apitypes:
    from: schema
    to: gen/apitypes

services:
  billing:
    spec: services/billing/api.yaml
    to: services/billing/internal/api
  identity:
    spec: services/identity/api.yaml
    to: services/identity/internal/api

generate: [types, validate, server, spec]

types:
  format:uuid:
    go: uuid.UUID
    import: github.com/google/uuid

errors:
  type_base_url: https://errors.acme.io/

unified:
  to: gen/openapi.yaml
  title: Acme Platform API
  version: "2026.1"
  servers:
    - { url: https://api.acme.io, description: Production }
  exclude: [internal-ops]
```

| Key | What it does |
|---|---|
| `shared.<name>.from` | A directory of component files, generated **once** into one Go package however many services reference it. |
| `shared.<name>.to` | Where that package is written. |
| `services.<name>.spec` | The service's root document. |
| `services.<name>.to` | Where its generated code and bundled `openapi.yaml` are written. |
| `generate` | The default target list. A service may override it with its own `generate`. |
| `types` | Go type overrides, keyed `format:<name>` for an OpenAPI format or by schema name for a single component. |
| `errors.type_base_url` | Prefixed to an error slug to form the RFC 9457 `type` URI. |
| `errors.validation_title` | Replaces the title of a validation failure. |
| `examples` | Where operations keep examples outside the standard places — see [Handler docs](handler-docs.md). A service may override it with its own. |
| `unified.*` | The platform-wide document — see below. |

Shared roots and services are keyed by name rather than listed, and the name is
the one the tool uses everywhere else: in diagnostics (`services.billing:`
rather than `packages[2]:`), and in `unified.exclude`, which names a service
instead of repeating its output path. The Go package name is the basename of
`to` — `apitypes`, `api` — overridable per entry with `package:`.

Targets are `types`, `validate`, `server`, `client`, `fake`, and `spec`. Naming
one pulls in what it needs: `server` implies `types`, `fake` implies `server`.
Relative paths resolve against the config file's directory.

Loaded through [Viper](https://github.com/spf13/viper): YAML, JSON, or TOML,
discovered by walking up from the working directory. Unknown keys are an error
rather than a silent no-op. A few settings can be overridden from the
environment with the `GOAPIGEN_` prefix, for example
`GOAPIGEN_ERRORS_TYPE_BASE_URL` — but generated code is committed, so an
override that differs from what CI regenerates with will fail `check`.

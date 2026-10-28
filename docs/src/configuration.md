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

## Scalar types

Without a `types` entry, a scalar maps to the Go type you would write by hand:

| OpenAPI | Go |
|---|---|
| `string` | `string` |
| `string`, `format: date-time` | `time.Time`: an RFC 3339 timestamp, `2026-10-27T09:30:00Z` |
| `string`, `format: date` | `date.Date`, from the runtime module: an RFC 3339 full-date, `2026-10-27` |
| `string`, `format: byte` or `binary` | `[]byte` |
| `integer` | `int`; `int32` and `int64` for those formats |
| `number` | `float64`; `float32` for `format: float` |
| `boolean` | `bool` |

Any other format, `time` and `uuid` among them, is the base type until a
`types` entry maps it.

A date is not a `time.Time`. A `time.Time` reads and writes a timestamp, so
it would turn `?from=2026-10-27` away and answer with a time and a zone the
contract never mentioned. `date.Date` reads exactly `YYYY-MM-DD`, rejecting a
timestamp or a day the calendar does not have, and writes the same form back,
in a parameter and in JSON alike. `date.Of(t)` and `d.In(loc)` convert to and
from a `time.Time`. A team that already has a civil-date type can map the
format to it instead:

```yaml
types:
  format:date:
    go: civil.Date
    import: cloud.google.com/go/civil
```

A mapped type is decoded from a parameter through its `UnmarshalText`, and to
and from JSON as encoding/json decides, so it needs `encoding.TextUnmarshaler`
and should agree with its own JSON form.

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

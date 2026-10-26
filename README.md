# go-api-gen

A spec-first OpenAPI code generator for Go, built for microservices monorepos.

An alternative to [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen/)
with one central idea: **a shared schema is authored once, generated once, and
imported by every service that uses it** — while each service still gets a
self-contained OpenAPI document for docs and gateways.

**[Read the documentation](https://franco-grobler.github.io/go-api-gen/)**

## Why

Generate Go from a bundled document and you cannot share a Go type: by the time
a generator reads it, the fact that `User` arrived from a shared file is gone,
so every service gets a private `type User struct` and code that spans two
services needs a conversion between identical types. go-api-gen makes
ownership part of the model instead. Billing's generated Go **imports** the
shared type:

```go
import "github.com/acme/monorepo/gen/apitypes"

type Invoice struct {
	ID     uuid.UUID      `json:"id"`
	Payer  apitypes.User  `json:"payer"`      // one Go type across every service
	Total  apitypes.Money `json:"total"`
	Status InvoiceStatus  `json:"status"`
}
```

while billing's bundled `openapi.yaml` **inlines** it, because a gateway cannot
follow a Go import.

From each service's spec it generates the types, validation as generated Go,
and the whole HTTP layer: routing on the standard library's `ServeMux`,
parameter and body decoding, security, and RFC 9457 errors. A handler receives
input that has already been checked.

## Install

```console
$ go install github.com/franco-grobler/go-api-gen/cmd/go-api-gen@latest
```

Generated code imports the runtime module, at the same version as the
generator:

```console
$ go get github.com/franco-grobler/go-api-gen/runtime@latest
```

## Quick start

A `go-api-gen.yaml` at the repository root, beside `go.mod`:

```yaml
version: 1

services:
  petstore:
    spec: api.yaml
    to: internal/api
```

```console
$ go-api-gen build     # generate every configured output
$ go-api-gen check     # fail if committed output is out of date, for CI
```

[`examples/`](examples) holds four runnable services, from one spec with no
shared schema to three separately built modules sharing one, and one built on
`oneOf` unions.

## Documentation

The [book](https://franco-grobler.github.io/go-api-gen/) covers the model,
configuration, the generated HTTP layer, and the runtime module, and how to
contribute and release. Its source is in [`docs/`](docs); `just docs` serves it
locally.

## License

MIT

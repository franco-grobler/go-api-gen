# go-api-gen

A spec-first OpenAPI code generator for Go, built for microservices monorepos.

An alternative to [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen/)
with one central idea: **a shared schema is authored once, generated once, and
imported by every service that uses it** — while each service still gets a
self-contained OpenAPI document for docs and gateways.

## The problem

In a monorepo, `oapi-codegen` gives you a choice between one enormous spec and
a pile of near-identical ones. Either way, every spec regenerates its own copy
of the shared types, so `User` in the billing service is a different Go type
from `User` in identity. `import-mapping` can paper over it, one entry at a
time, by hand.

## The model

Shared components live in a schema root. Service specs are thin and `$ref`
into it.

```
api/
  go-api-gen.yaml
  shared/
    user.yaml                # User, UserStatus, Address
    common.yaml              # Money, PageInfo, shared parameters
  services/
    billing/api.yaml         # paths; $ref ../../shared/user.yaml#/...
    identity/api.yaml
```

```yaml
# api/go-api-gen.yaml
version: 1

schemas:
  - path: shared
    output: ../gen/apitypes     # one Go package, generated once

formats:
  uuid: { type: uuid.UUID, import: github.com/google/uuid }

errors:
  type_base_url: https://errors.acme.io/

packages:
  - spec: services/billing/api.yaml
    output: ../services/billing/internal/api
```

```console
$ go-api-gen build
  ../gen/apitypes/types.gen.go
  ../services/billing/internal/api/types.gen.go
  ../services/billing/internal/api/openapi.yaml
  ../services/identity/internal/api/openapi.yaml
go-api-gen: wrote 4 of 4 files
```

Billing's generated code **imports** the shared type rather than redeclaring it:

```go
import "github.com/acme/monorepo/gen/apitypes"

type Invoice struct {
	ID     uuid.UUID      `json:"id"`
	Payer  apitypes.User  `json:"payer"`      // one Go type across every service
	Total  apitypes.Money `json:"total"`
	Status InvoiceStatus  `json:"status"`
}
```

Its `openapi.yaml`, meanwhile, is fully self-contained and tree-shaken: the
shared schemas it reaches are inlined as components, shared parameters are
inlined at their use sites, and shared schemas it never touches are left out.

## Optional fields

Optional and nullable fields use `Optional[T]`, which distinguishes the three
states JSON actually has. A pointer can only express two of them, which is why
pointer-based models cannot implement PATCH correctly.

```go
type UpdateUser struct {
	Name  optional.Optional[string] `json:"name,omitzero"`
	Email optional.Optional[string] `json:"email,omitzero"`
}

req.Name.IsSet()    // was the key present at all?
req.Email.IsNull()  // present and explicitly null: clear the field
v, ok := req.Name.Get()
```

## The unified spec

`build` gives each service its own document. `unify` stitches them into one
platform-wide document, for an API gateway, a docs portal, or a client that
spans services.

```yaml
unified:
  output: ../gen/openapi.yaml
  title: Acme Platform API
  version: "2026.1"
  servers:
    - { url: https://api.acme.io, description: Production }
  # exclude: [../services/internal-ops/internal/api]
```

```console
$ go-api-gen unify
go-api-gen: wrote ../gen/openapi.yaml

$ go-api-gen unify --stdout | redocly lint -
```

Paths merge method by method, so two services may serve different methods of
the same path — but the same one is a conflict, and so is a reused
`operationId`. Both are refused with a message naming each contributing spec.
Operations that declare no tags of their own are tagged with their service's
title, so a docs renderer groups the result by service rather than showing one
flat list of every endpoint in the monorepo.

## The HTTP layer

`build` generates the whole path from socket to handler: route registration on
the stdlib `ServeMux`, parameter parsing, body decoding, and validation. A
handler receives input that has already been checked.

```go
type Handler interface {
	CreateInvoice(ctx context.Context, req CreateInvoiceRequest) (*Invoice, error)
}

srv := api.NewServer(myHandler,
	api.WithErrorOptions(apierr.Options{
		TypeBaseURL: "https://errors.acme.io/",
		TraceID:     tracing.FromRequest,
	}),
	api.WithMiddleware(auth, logging),
)
```

Validation is generated Go, not tags read by reflection at runtime:

```go
func (money Money) Validate(v *validate.V) {
	for _, name := range money.missingFields {
		v.Field(name).Fail("missing", "Field required")
	}
	if utf8.RuneCountInString(money.Currency) < 3 {
		v.Field("currency").Fail("string_too_short",
			"String should have at least 3 characters", "min_length", 3)
	}
}
```

Required fields get a generated `UnmarshalJSON` that records which keys the
client actually sent, so an omitted field is reported as missing while a
deliberate `0` or `""` is not.

### Custom rules

`x-validate` on a schema or property declares a rule you write in Go. The
generator emits an interface and asserts the type satisfies it, so a rule named
in the spec but never written is a compile error:

```yaml
email:
  type: string
  x-validate: [corporate_domain]
```

```go
// generated
type UserValidator interface {
	ValidateEmailCorporateDomain(v *validate.V)
}
var _ UserValidator = User{}

// yours, in a file the generator does not own
func (u User) ValidateEmailCorporateDomain(v *validate.V) {
	if !strings.HasSuffix(u.Email, "@acme.com") {
		v.Fail("corporate_domain", "Email must be on the acme.com domain")
	}
}
```

Rules are pure functions of the value. A check that needs a database belongs in
the handler, using the same error builder so the response is identical:

```go
if taken, _ := db.EmailTaken(ctx, req.Body.Email); taken {
	return nil, apierr.Validation().
		Field("body", "email").
		Fail("email_taken", "already registered")
}
```

### Errors

Every failure — schema constraint, custom rule, or handler-raised — leaves in
one RFC 9457 shape:

```json
HTTP 422  application/problem+json
{
  "type": "https://errors.acme.io/validation_failed",
  "title": "Request validation failed",
  "status": 422,
  "detail": "2 fields failed validation",
  "instance": "/v1/invoices",
  "trace_id": "01J8X...",
  "errors": [
    {"type": "missing", "loc": ["body", "payer_id"], "msg": "Field required"},
    {"type": "string_too_long", "loc": ["body", "total", "currency"],
     "msg": "String should have at most 3 characters",
     "ctx": {"max_length": 3}}
  ]
}
```

An unexpected error becomes a 500 whose detail is withheld from the caller and
logged instead, so a database message cannot leak through by accident.

## Commands

```
go-api-gen build     Generate every configured output
go-api-gen check     Report whether committed output is up to date (for CI)
go-api-gen unify     Stitch every service spec into one unified document
```

The CLI is built on [Cobra](https://github.com/spf13/cobra), so
`go-api-gen completion <shell>` produces a shell completion script.

Generated output is deterministic, so `check` is a reliable drift gate.

## Configuration

Loaded through [Viper](https://github.com/spf13/viper): YAML, JSON, or TOML,
discovered by walking up from the working directory. Unknown keys are an error
rather than a silent no-op. A few settings can be overridden from the
environment with the `GOAPIGEN_` prefix, for example
`GOAPIGEN_ERRORS_TYPE_BASE_URL`.

## Status

Under construction. Working today:

- [x] Cross-file `$ref` resolution with shared schema identity
- [x] Ownership model: shared roots vs. service-local schemas, with collision
      and cross-service-leak detection
- [x] Tree-shaken, self-contained bundled specs, per service and unified
- [x] Type generation: structs, enums, `allOf` embedding, inline type
      synthesis, `Optional[T]`, configurable format mapping
- [x] `check` drift gate
- [x] Generated validation from spec constraints, with required-field
      presence tracking
- [x] `x-validate` custom rules with compile-enforced interfaces
- [x] RFC 9457 error package and middleware
- [x] Server: routing, parameter parsing, body decoding, typed handlers
- [ ] Typed client
- [ ] Test fakes

`oneOf`/`anyOf` are rejected with a clear error rather than generated badly.

## Development

```console
$ make            # build, lint, test, and verify generated output
$ make test       # go test -race -shuffle=on, both modules
$ make lint       # golangci-lint, both modules
$ make generate   # regenerate the example monorepo
$ make fix        # apply the linter's automatic fixes
```

CI runs four jobs: build and test (`-race -shuffle=on`, with coverage), lint,
module tidiness, and a generated-code gate that regenerates the example, fails
on any drift, and then compiles the result — because output that diffs clean
but does not build would pass every other check.

Linting is strict. Hand-written code is held to 80 columns; generated files are
excluded, since gofmt's struct-tag alignment has no reason to respect a limit
meant for human readers. Three exceptions are configured deliberately, each
with its reason in `.golangci.yml`: `govet`'s `shadow`, `staticcheck`'s ST1005
(several diagnostics here are deliberately two sentences — the problem, then
the fix), and `gosec`'s G304 (reading files the user names is what a generator
does).

The linter version is pinned in both `Makefile` and `.github/workflows/ci.yml`;
keep them in step.

## The runtime module

Generated code is thin. Everything that does not vary between services — the
`Optional[T]` type, the validation accumulator, the error envelope and its
middleware, and the server plumbing — lives in one module you install once:

```console
$ go get github.com/franco-grobler/go-api-gen/runtime
```

| Package | What it holds |
|---|---|
| `runtime/optional` | `Optional[T]`: absent / null / set |
| `runtime/validate` | `V`, the field-error accumulator, and `FieldError` |
| `runtime/apierr` | `Problem`, the error builders, and the middleware |
| `runtime/httpx` | Server options, middleware ordering, response writing |

It is a **separate module** from the generator, so depending on the generated
code does not drag in the generator's spec parser, Viper, or Cobra. It has no
dependencies of its own.

The generator emits only what actually differs per service: the `Handler`
interface, the request and response types, the routing table, and the decode
path for each operation. A shared helper is never copied into a service — that
would be the same duplication this tool exists to remove from schemas.

## Layout

```
cmd/go-api-gen/     the CLI
internal/           config, spec loading, ownership, emitters
runtime/            a separate module: what your services import
examples/monorepo/  two services sharing a schema root, compiled in CI
```

## License

MIT

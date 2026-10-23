# go-api-gen

A spec-first OpenAPI code generator for Go, built for microservices monorepos.

An alternative to [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen/)
with one central idea: **a shared schema is authored once, generated once, and
imported by every service that uses it** — while each service still gets a
self-contained OpenAPI document for docs and gateways.

## The problem

Generate Go from a bundled document and you cannot share a Go type.

Bundling is what a gateway and a docs portal need: `redocly bundle` walks every
`$ref` and inlines it, so the output depends on no other file. But by the time
a generator reads that document, the fact that `User` arrived from a shared
file is gone. Each service gets a private `type User struct`, so billing's
`User` and identity's `User` are distinct Go types with identical fields, and
any code that spans both needs a conversion function.

`oapi-codegen` offers two ways around this. Both work; both charge for it.

- **`import-mapping`** generates from the *unbundled* spec, where the external
  reference is still visible, and maps that document onto a Go import path. The
  keys are matched against the reference string exactly as written, so
  `../../pkg/schemas/openapi.yaml` and `../../../../../pkg/schemas/openapi.yaml`
  are two different keys for the same package. Every relative spelling in the
  tree needs its own entry, and the two consumers of the spec drift apart:
  documentation comes from the bundle, code from the source.
- **`x-go-type`** travels inside the document and survives bundling, so the
  pipeline keeps its shape. But a shared package generated from its own
  annotated spec imports itself, which does not compile. The shared types have
  to be hand-written, and the schema stops being their source of truth.

Both are the same concession: sharing is bolted onto a pipeline whose only
artefact is a bundle. go-api-gen makes ownership part of the model instead, and
produces the bundle separately, for the readers that actually need one.

## The model

Shared components live in a schema root, owned by no service. Each service's
spec lives beside the code that serves it and `$ref`s into that root.

```
go-api-gen.yaml              at the repo root, beside go.mod
schema/
  user.yaml                  User, UserStatus, Address
  common.yaml                Money, PageInfo, shared parameters
services/
  billing/
    api.yaml                 the contract
    service.go               the handlers you write
    internal/api/            generated from it
  identity/
    api.yaml
    service.go
    internal/api/
gen/
  apitypes/                  the shared schemas, as one Go package
  openapi.yaml               the unified document
```

References are ordinary relative `$ref`s, so the source tree stays legible to
every other OpenAPI tool — `redocly lint services/billing/api.yaml` reads it
unchanged:

```yaml
payer:
  $ref: ../../schema/user.yaml#/components/schemas/User
```

Two outputs come out of each service, and they bundle differently on purpose.
Billing's generated Go **imports** the shared type:

```go
import "github.com/acme/monorepo/gen/apitypes"

type Invoice struct {
	ID     uuid.UUID      `json:"id"`
	Payer  apitypes.User  `json:"payer"`      // one Go type across every service
	Total  apitypes.Money `json:"total"`
	Status InvoiceStatus  `json:"status"`
}
```

Billing's `openapi.yaml` **inlines** it, because a gateway cannot follow a Go
import. The document is self-contained and tree-shaken: shared schemas it
reaches become components, shared parameters are inlined at their use sites,
and shared schemas it never touches are left out entirely.

Ownership is checked rather than assumed. A schema reached by two services must
live in a shared root, and a Go name claimed twice is an error that names both
claimants.

## Configuration

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
| `examples` | Where operations keep examples outside the standard places — see [Handler docs](#handler-docs). A service may override it with its own. |
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

```console
$ go-api-gen build
  gen/apitypes/types.gen.go
  services/billing/internal/api/types.gen.go
  services/billing/internal/api/openapi.yaml
  services/identity/internal/api/openapi.yaml
go-api-gen: wrote 4 of 4 files

$ go-api-gen unify
go-api-gen: wrote gen/openapi.yaml

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

srv := api.NewServer(myHandler, api.WithMiddleware(auth, logging))
```

The `errors` block of the config is already applied — `NewServer` seeds itself
with it. To set something the config cannot state, start from that value
rather than a fresh `apierr.Options`, since `WithErrorOptions` replaces the
whole struct:

```go
opts := api.DefaultErrorOptions()   // type_base_url, validation_title
opts.TraceID = tracing.FromRequest

srv := api.NewServer(myHandler,
	api.WithErrorOptions(opts),
	api.WithMiddleware(auth, logging),
)
```

### Handler docs

Each `Handler` method is documented with its operation's summary and
description, and one example request and response: what the handler is given,
and what it should produce.

```go
type Handler interface {
	// CreatePet: Create a pet.
	//
	// Example request (A dog):
	//
	//	{
	//	  "name": "Rex",
	//	  "kind": "dog"
	//	}
	CreatePet(ctx context.Context, req CreatePetRequest) (*Pet, error)
}
```

Examples come from the standard places first: `example` or `examples` on the
JSON request body and the success response. A document that keeps them
elsewhere can say where, as OpenAI's does under `x-oaiMeta`:

```yaml
examples:
  path: x-oaiMeta.examples   # from each operation, to one example or a list
  request: request.curl      # from each example; default "request"
  response: response         # default "response"
  title: title               # default "title"
```

Each value is a dot-separated path. Quote a segment that contains a dot, as in
`request."node.js"`, and use a number to index a list. A configured example
fills in only the side the standard ones leave empty. An object, or a string
holding JSON, is shown as indented JSON; a curl command contributes its `-d`
body when that is JSON, and any other text is shown as written.

When a schema already has an operation's `<Op>Request` name, as OpenAI's
`CreateAssistantRequest` does, the request struct is named `<Op>Input`
instead.

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
deliberate `0` or `""` is not. A field that was never sent is reported once:
its other constraints are skipped rather than measured against the zero value,
so one absent input does not produce one error per keyword.

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

### Security

`securitySchemes` and `security` are read, and generate an `Authenticator` the
server calls before any handler runs. The generator locates the credential
where the specification says it lives; what a token *means* is not something a
specification can say, so that part is yours.

```yaml
security:
  - bearerAuth: []          # the document-wide default
paths:
  /login:
    post:
      security: []          # overrides it: this is how you get a token
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
```

```go
// generated
type Authenticator interface {
	BearerAuth(ctx context.Context, r *http.Request, token string) (context.Context, error)
}

func NewServer(h Handler, a Authenticator, opts ...ServerOption) http.Handler
```

The authenticator is a parameter, not an option: a server that could be built
without one would route requests nobody had checked. The context it returns is
the one the handler receives, which is how the caller's identity gets there —
and the only way it does.

```go
func (t *Tokens) BearerAuth(
	ctx context.Context, _ *http.Request, token string,
) (context.Context, error) {
	caller, ok := t.callers[token]
	if !ok {
		return nil, apierr.Unauthorized("this token is not recognised")
	}
	return context.WithValue(ctx, callerKey{}, caller), nil
}
```

Returning an error refuses the request; an `*apierr.Problem` is rendered as it
stands, so a handler that wants 403 rather than 401 says so. A missing
credential is a 401 carrying the `WWW-Authenticate` challenge RFC 9110
requires.

`apiKey` (header, query, or cookie), `http` (bearer, basic, or any registered
scheme), `oauth2`, `openIdConnect`, and `mutualTLS` are supported. Several
schemes in one requirement are an AND, several requirements an OR, and an
empty requirement (`- {}`) admits a caller who presents nothing — one who
presents a credential is still identified by it. `oauth2` and `openIdConnect`
methods receive the scopes the operation asked for.

Authentication is who the caller is; authorization is whether they may do this,
and stays in the handler where the data is. See
`examples/microservices/identity`.

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
- [x] `security`: schemes, requirements, and a compile-enforced
      `Authenticator`
- [x] Named `shared`/`services` config, with targets implying their
      prerequisites
- [ ] Selecting one service on the command line: `go-api-gen build billing`
- [ ] Typed client
- [ ] Test fakes

`oneOf`/`anyOf` are rejected with a clear error rather than generated badly.

One validation gap is **silent** — the constraint is accepted by the parser
and never applied, so the generated server lets the value through. Do not rely
on it until this says otherwise: `multipleOf`, `uniqueItems`, `const`, and
`additionalProperties: false`, which does not reject unknown fields.

## Development

All three examples are runnable programs, not snippets. Each has a binary
under `cmd/`, a map behind a mutex standing in for the database, request
logging through `WithMiddleware`, and a shutdown that drains in-flight
requests.

The smallest is `examples/petstore` — one spec, one generated package, no
shared schema root:

```
examples/petstore/
  go-api-gen.yaml
  api.yaml                the contract
  cmd/petstored/main.go   flags, logger, listener, shutdown
  service.go              the handlers you write
  store.go                the database: a map
  internal/api/           generated from api.yaml
```

```console
$ cd examples/petstore && go run ./cmd/petstored
$ curl -s -X POST localhost:8080/pets \
    -H 'content-type: application/json' -d '{"name":"Fido","tag":"dog"}'
{"id":1,"name":"Fido","tag":"dog"}
```

`examples/microservices` is the other end: two services that are built and
deployed separately, with the shared schema a third module they both require.
One configuration governs all three, because the contract is platform-wide
even when the code is not — each output directory resolves its import path
against whichever `go.mod` encloses it.

```
examples/microservices/
  go-api-gen.yaml     one config, three modules
  openapi.yaml        the platform document, owned by no service
  schema/             module: the contract
    openapi/*.yaml
    apitypes/         generated, and required by both services below
  identity/           module: cmd/identityd, requires schema
  billing/            module: cmd/billingd, requires schema
```

`billing.Invoice.Payer` is `apitypes.User` — the type from the other module,
not a copy of it. Run both and the round trip is the point:

```console
$ (cd identity && go run ./cmd/identityd) &
$ (cd billing && go run ./cmd/billingd) &
$ curl -s localhost:8081/users/5b0a3f2e-0000-4000-8000-000000000001 \
    | jq '{payer: ., total: {amount_minor: 1500, currency: "GBP"}}' \
    | curl -s -X POST localhost:8080/invoices \
        -H 'content-type: application/json' -d @-
{"id":"inv_001","payer":{"id":"5b0a3f2e-...","email":"ada@example.com",
 "status":"active"},"total":{"amount_minor":1500,"currency":"GBP"}}
```

Identity marshalled that payer and billing decoded it, from a different
module, with no conversion type in between.

`examples/monorepo` sits between the two: one module, one binary per service
under `cmd/`, and `internal/platform` holding the listener and shutdown that
both share — which is the plainest thing a monorepo buys, and the reason the
microservices example carries a copy per module instead.

Recipes live in a [justfile](https://just.systems); `just --list` prints them.

```console
$ just            # build, lint, test, and verify generated output
$ just test       # go test -race -shuffle=on, every module
$ just lint       # golangci-lint, the generator and the runtime
$ just generate   # regenerate every example
$ just tidy       # go mod tidy, every module, against its own requirements
$ just fix        # apply the linter's automatic fixes
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

The linter version is pinned in both `justfile` and
`.github/workflows/ci.yml`; keep them in step. `just lint` reinstalls into
`.bin` when the pinned version and the installed one disagree, so a bump takes
effect without a manual `just clean`.

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
cmd/go-api-gen/          the CLI
internal/                config, spec loading, ownership, emitters
runtime/                 a separate module: what your services import
examples/petstore/       one service, no shared root
examples/monorepo/       two services in one module, sharing a schema root
examples/microservices/  the same two, as three separately built modules
```

Each example carries the specs, the generated code, and the handlers you would
write, and all three are built and tested in CI.

## License

MIT

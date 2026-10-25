# Examples

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

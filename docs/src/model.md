# The model

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

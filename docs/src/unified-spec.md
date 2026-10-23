# The unified spec

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

# The unified spec

Each service gets its own document. The unified spec stitches them into one
platform-wide document, for an API gateway, a docs portal, or a client that
spans services. With `unified.to` set, `build` writes it with everything else,
and `check` fails when it is missing or out of date, as it does for any other
generated file:

```console
$ go-api-gen build
  gen/apitypes/types.gen.go
  gen/openapi.yaml
  services/billing/internal/api/types.gen.go
  services/billing/internal/api/openapi.yaml
  services/identity/internal/api/openapi.yaml
go-api-gen: wrote 5 of 5 files
```

`unify` writes the same document on its own, and can put it elsewhere or pipe
it to another tool:

```console
$ go-api-gen unify --output dist/platform.yaml
go-api-gen: wrote dist/platform.yaml

$ go-api-gen unify --stdout | redocly lint -
```

Paths merge method by method, so two services may serve different methods of
the same path — but the same one is a conflict, and so is a reused
`operationId`. Both are refused with a message naming each contributing spec.
Operations that declare no tags of their own are tagged with their service's
title, so a docs renderer groups the result by service rather than showing one
flat list of every endpoint in the monorepo.

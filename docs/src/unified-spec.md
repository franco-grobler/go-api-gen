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

## OpenAPI 3.0 services

The unified document declares the highest OpenAPI version among its sources,
and never less than 3.1. A service written in 3.0 still contributes, but 3.1
has no `nullable` keyword: its schemas are JSON Schema, where a value may be
`null` only when the schema says so in JSON Schema terms. Copied as written, a
3.0 `nullable: true` would mean nothing, and a client generated from the
unified document would refuse the `null` the service accepts. So the schemas of
a 3.0 source are translated as they are copied:

| OpenAPI 3.0                                       | Unified document                                   |
| ------------------------------------------------- | -------------------------------------------------- |
| `{type: string, nullable: true}`                  | `{type: [string, "null"]}`                         |
| `{type: string, enum: [a, b], nullable: true}`    | `{type: [string, "null"], enum: [a, b, null]}`     |
| `{oneOf: [A, B], nullable: true}`, or `anyOf`     | `{oneOf: [A, B, {type: "null"}]}`                  |
| `{allOf: [A], nullable: true}`                    | `{anyOf: [A, {type: "null"}]}`                     |
| `{allOf: [A, {nullable: true}]}`                  | `{anyOf: [A, {type: "null"}]}`                     |
| any other schema with `nullable: true`            | `{anyOf: [the schema, {type: "null"}]}`            |
| `nullable: false`                                 | dropped                                            |

A description, title, default, or example stays on the outside of an `anyOf`
made this way. A file that declares no `openapi` version of its own, such as a
schema in a file by itself, is in the version of the document that references
it. A 3.1 source is copied as written.

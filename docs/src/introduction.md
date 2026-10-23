# Introduction

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

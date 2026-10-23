# The runtime module

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

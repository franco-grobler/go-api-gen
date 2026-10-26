# Status

Under construction. Working today:

- [x] Cross-file `$ref` resolution with shared schema identity
- [x] Ownership model: shared roots vs. service-local schemas, with collision
      and cross-service-leak detection
- [x] Tree-shaken, self-contained bundled specs, per service and unified
- [x] Type generation: structs, enums, `allOf` embedding, inline type
      synthesis, `Optional[T]`, configurable format mapping
- [x] `oneOf`/`anyOf` as union types, chosen by discriminator or by shape
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

A union beside `properties` or `allOf`, a union as an `allOf` member or a
parameter, and `oneOf` with `anyOf` on one schema are rejected with a clear
error; see [Unions](unions.md).

Array and object parameters are read in the styles OpenAPI defines for
them: `form` (exploded or not), `spaceDelimited`, `pipeDelimited`, and
`deepObject` in the query, and `simple` in a path or header. An array of
arrays or objects, an object with an array or object property, and the
`label` and `matrix` path styles are rejected with a clear error.

One validation gap is **silent** — the constraint is accepted by the parser
and never applied, so the generated server lets the value through. Do not rely
on it until this says otherwise: `multipleOf`, `uniqueItems`, `const`, and
`additionalProperties: false`, which does not reject unknown fields.

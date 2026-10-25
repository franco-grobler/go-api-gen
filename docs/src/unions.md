# Unions

A `oneOf` or `anyOf` becomes a struct with one pointer field per member, of
which exactly one is set:

```yaml
Pet:
  oneOf:
    - $ref: '#/components/schemas/Cat'
    - $ref: '#/components/schemas/Dog'
  discriminator:
    propertyName: pet_type
```

```go
// Pet is one of Cat or Dog: exactly one field is set.
type Pet struct {
	Cat *Cat
	Dog *Dog
}

switch {
case req.Body.Pet.Cat != nil:
	// ...
case req.Body.Pet.Dog != nil:
	// ...
}

return api.Pet{Dog: &dog}
```

A member that is a named schema gives the field its name. An inline member
is named by its `title`, or else by the kind of value it holds: `String`,
`Integer`, `StringList`, `Object`, and so on, with a number added if two
would share a name.

Encoding writes whichever member is set. A union with no member set fails to
encode, unless the union is nullable, in which case it encodes as `null`.

## Choosing the member

Decoding a union means working out which member the JSON is, and how depends
on whether the schema has a discriminator.

**With a discriminator**, the value of its property picks the member. Each
member's value comes from the first of these that applies:

1. an entry in `discriminator.mapping`;
2. the single value the member's own discriminator property is fixed to, by
   `enum` or `const`. Many documents leave out the mapping and rely on this,
   rather than on the schema name the specification says to fall back to;
3. the member's schema name.

When two members end up with the same value, the discriminator cannot choose
between them, and the union is decoded by shape instead.

**Without a discriminator**, the JSON is matched against the shape of each
member. A member fits when:

- the value is a JSON kind the member accepts;
- for an enum or `const`, the value is one of those it allows;
- for an object, every required key is present, no key is undeclared when
  `additionalProperties` is `false`, and every property restricted by `enum`
  or `const` (such as a `type: {enum: [function]}`) holds an allowed value.

When more than one member fits, the most specific wins: for an object, the
member that declares the most of its keys, which is how Pydantic's smart mode
settles a union too. A tie is an error for `oneOf`. For `anyOf`, the first
member in the tie wins.

Only the shape is used to choose. Once a member is chosen, its full
validation runs as usual, and failures are reported inside the union's
location.

### How this differs from JSON Schema

JSON Schema defines both keywords by full validation. A value is valid under
`oneOf` when it passes every rule of exactly one member, and under `anyOf`
when it passes at least one. A discriminator is only a hint for which member
to check. The generated code departs from that in two ways:

- **It chooses by shape, not by every rule.** Formats, lengths, bounds and
  nested properties play no part in the choice. Two members that differ only
  in those look the same, so a value can be decoded as the wrong one and
  fail that member's validation, or be reported as `union_ambiguous` when
  only one member would accept it. Give such a union a discriminator, or a
  property fixed by `enum` or `const` in each member.
- **It accepts a `oneOf` value that fits more than one member,** when one of
  them declares more of its keys. JSON Schema would reject the value. A Go
  value can only hold one member, and real documents, OpenAI's among them,
  have `oneOf` members that overlap, so the more specific one is taken
  rather than the request refused.

A union whose members can be told apart by kind, required keys or a fixed
property, which is most of them, is decided exactly as JSON Schema would
decide it.

## Errors

A value that is none of the members is not a decoding error: it is reported
like any other validation failure, at the union's location, with one of
these types:

| Type | When |
|---|---|
| `union_tag_not_found` | the discriminator property is missing, or not a string |
| `union_tag_invalid` | the discriminator names no member |
| `union_no_match` | no member fits the value |
| `union_ambiguous` | a `oneOf` value fits more than one member equally |

```json
{"type": "union_tag_invalid", "loc": ["body", "pet"],
 "msg": "Input tag 'bird' found using 'pet_type' does not match any of the expected tags: 'Dog', 'cat'",
 "ctx": {"discriminator": "'pet_type'", "tag": "bird", "expected_tags": "'Dog', 'cat'"}}
```

## Null

A `{type: 'null'}` member makes the union nullable rather than adding a
member. A union that is left with a single member is that member, so
`anyOf: [{$ref: User}, {type: 'null'}]` is a nullable `User`:
`Optional[User]` on a property. `allOf: [{$ref: User}, {nullable: true}]`,
the OpenAPI 3.0 way of writing the same thing, means the same.

## Not supported yet

Each of these is rejected with an error naming the schema:

- `oneOf` or `anyOf` beside `properties` or `allOf`. Move the shared
  properties into each member.
- `oneOf` and `anyOf` on the same schema.
- a union as an `allOf` member.
- a union as a parameter.
- an inline member of a discriminated union that has no discriminator value.

`x-go-type` on the union replaces all of this with a type of your own.

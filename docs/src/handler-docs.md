# Handler docs

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

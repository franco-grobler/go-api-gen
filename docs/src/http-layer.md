# The HTTP layer

`build` generates the whole path from socket to handler: route registration on
the stdlib `ServeMux`, parameter parsing, body decoding, and validation. A
handler receives input that has already been checked.

```go
type Handler interface {
	CreateInvoice(ctx context.Context, req CreateInvoiceRequest) (*Invoice, error)
}

srv := api.NewServer(myHandler, api.WithMiddleware(auth, logging))
```

The `errors` block of the config is already applied — `NewServer` seeds itself
with it. To set something the config cannot state, start from that value
rather than a fresh `apierr.Options`, since `WithErrorOptions` replaces the
whole struct:

```go
opts := api.DefaultErrorOptions()   // type_base_url, validation_title
opts.TraceID = tracing.FromRequest

srv := api.NewServer(myHandler,
	api.WithErrorOptions(opts),
	api.WithMiddleware(auth, logging),
)
```

## Parameters

Path, query and header parameters are parsed into their declared types before
the handler runs, and a value that does not parse is a 422 at the parameter's
own location, such as `["query", "from"]`, reported with every other failure
in the request. Each type has its own failure:

| Declared as | Accepts | Failure |
|---|---|---|
| `integer` | `42` | `int_parsing` |
| `number` | `4.2` | `float_parsing` |
| `boolean` | `true`, `false` | `bool_parsing` |
| `format: date` | `2026-10-27` | `date_parsing` |
| `format: date-time` | `2026-10-27T09:30:00Z` | `datetime_parsing` |
| `format: uuid`, when mapped | a UUID | `uuid_parsing` |

A date is only a date: `?from=2026-10-27T00:00:00Z` is a `date_parsing`
failure, as is `2026-02-29`. See [scalar types](configuration.md#scalar-types).

## Request bodies

A JSON request body is decoded into the request struct's `Body` field and
checked against its schema before the handler runs. A body is *absent* when
it holds no bytes at all: a `Content-Length` of 0, or a chunked body that ends
before its first byte. The length header alone cannot tell, so the generated
code reads up to the first byte to find out (`httpx.Body`).

| The request sends              | `required: true`                      | `required: false` (the default)  |
| ------------------------------ | ------------------------------------- | -------------------------------- |
| no body                        | 422, `missing` at `["body"]`          | the handler runs, `Body` unset   |
| bytes that are not JSON        | 422, `json_invalid` at `["body"]`     | the same                         |
| only whitespace                | 422, `json_invalid` at `["body"]`     | the same                         |
| JSON that breaks the schema    | 422, each failure at its own location | the same                         |
| `null`                         | decoded as the zero value, then checked | `Body` set and null, not checked |

A body of only whitespace is not absent: it is not JSON, so it is reported
as such rather than quietly treated as missing.

An optional body is an `optional.Optional`, so a handler can tell a request
sent without a body from one sent with an empty object:

```go
type VoidInvoiceRequest struct {
	Path VoidInvoicePathParams
	// Body is unset when the request is sent without one.
	Body optional.Optional[VoidInvoice]
	Raw  *http.Request
}

func (s *Service) VoidInvoice(ctx context.Context, req api.VoidInvoiceRequest) (*api.Invoice, error) {
	if body, ok := req.Body.Get(); ok {
		// the client sent a body, and it passed validation
	}
	...
}
```

A required body is the schema's type itself, since it is always there by the
time the handler runs.

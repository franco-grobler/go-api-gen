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

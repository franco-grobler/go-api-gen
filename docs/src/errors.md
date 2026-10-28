# Errors

Every failure — schema constraint, custom rule, or handler-raised — leaves in
one RFC 9457 shape:

```json
HTTP 422  application/problem+json
{
  "type": "https://errors.acme.io/validation_failed",
  "title": "Request validation failed",
  "status": 422,
  "detail": "2 fields failed validation",
  "instance": "/v1/invoices",
  "trace_id": "01J8X...",
  "errors": [
    {"type": "missing", "loc": ["body", "payer_id"], "msg": "Field required"},
    {"type": "string_too_long", "loc": ["body", "total", "currency"],
     "msg": "String should have at most 3 characters",
     "ctx": {"max_length": 3}}
  ]
}
```

An unexpected error becomes a 500 whose detail is withheld from the caller and
logged instead, so a database message cannot leak through by accident.

A request body that cannot be decoded is reported at `["body"]` itself:
`json_invalid` when the bytes are not JSON, and `missing` when a body the
operation requires was not sent at all. See
[request bodies](http-layer.md#request-bodies) for what counts as absent.

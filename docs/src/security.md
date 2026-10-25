# Security

`securitySchemes` and `security` are read, and generate an `Authenticator` the
server calls before any handler runs. The generator locates the credential
where the specification says it lives; what a token *means* is not something a
specification can say, so that part is yours.

```yaml
security:
  - bearerAuth: []          # the document-wide default
paths:
  /login:
    post:
      security: []          # overrides it: this is how you get a token
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
```

```go
// generated
type Authenticator interface {
	BearerAuth(ctx context.Context, r *http.Request, token string) (context.Context, error)
}

func NewServer(h Handler, a Authenticator, opts ...ServerOption) http.Handler
```

The authenticator is a parameter, not an option: a server that could be built
without one would route requests nobody had checked. The context it returns is
the one the handler receives, which is how the caller's identity gets there —
and the only way it does.

```go
func (t *Tokens) BearerAuth(
	ctx context.Context, _ *http.Request, token string,
) (context.Context, error) {
	caller, ok := t.callers[token]
	if !ok {
		return nil, apierr.Unauthorized("this token is not recognised")
	}
	return context.WithValue(ctx, callerKey{}, caller), nil
}
```

Returning an error refuses the request; an `*apierr.Problem` is rendered as it
stands, so a handler that wants 403 rather than 401 says so. A missing
credential is a 401 carrying the `WWW-Authenticate` challenge RFC 9110
requires.

`apiKey` (header, query, or cookie), `http` (bearer, basic, or any registered
scheme), `oauth2`, `openIdConnect`, and `mutualTLS` are supported. Several
schemes in one requirement are an AND, several requirements an OR, and an
empty requirement (`- {}`) admits a caller who presents nothing — one who
presents a credential is still identified by it. `oauth2` and `openIdConnect`
methods receive the scopes the operation asked for.

Authentication is who the caller is; authorization is whether they may do this,
and stays in the handler where the data is. See
`examples/microservices/identity`.

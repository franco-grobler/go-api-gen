package identity

import (
	"context"
	"net/http"
	"sync"

	"github.com/example/microservices/identity/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// Tokens is identity's authenticator: a map from bearer token to the user it
// identifies, which is the same in-memory stand-in the stores use.
//
// A real service would verify a signature or call an introspection endpoint.
// What the generator settles is only that this runs before any handler, with
// the token the specification said to look for, and that whatever context it
// returns is the one the handler is given.
type Tokens struct {
	mu      sync.RWMutex
	callers map[string]string
}

// The generated interface is the contract here too: a scheme named in the spec
// and not implemented is a compile error in this file.
var _ api.Authenticator = (*Tokens)(nil)

// NewTokens returns an authenticator that accepts the given token/user pairs.
func NewTokens(pairs map[string]string) *Tokens {
	callers := make(map[string]string, len(pairs))
	for token, user := range pairs {
		callers[token] = user
	}
	return &Tokens{callers: callers}
}

// callerKey is unexported so no other package can collide with it, which is
// the same reason apierr keeps its own context key private.
type callerKey struct{}

// BearerAuth resolves a token to the user it belongs to.
//
// Returning an error refuses the request: an *apierr.Problem is rendered as it
// stands, so this chooses 401 rather than being given one.
func (t *Tokens) BearerAuth(
	ctx context.Context, _ *http.Request, token string,
) (context.Context, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	caller, ok := t.callers[token]
	if !ok {
		return nil, apierr.Unauthorized("this token is not recognised")
	}
	return context.WithValue(ctx, callerKey{}, caller), nil
}

// Caller returns the user id the authenticator resolved, and whether there was
// one. A handler reaches its caller through this and nothing else.
func Caller(ctx context.Context) (string, bool) {
	caller, ok := ctx.Value(callerKey{}).(string)
	return caller, ok
}

// Package identity serves the platform's users.
//
// It is its own Go module. The types it serves come from
// github.com/example/microservices/schema, a module it requires and does not
// own; internal/api is generated from api.yaml.
package identity

import (
	"context"
	"net/http"

	"github.com/example/microservices/identity/internal/api"
	"github.com/example/microservices/schema/apitypes"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// Service implements the generated handler interface over a store.
type Service struct {
	db *store
}

// The generated interface is the contract. Asserting it here turns a spec
// change that alters a signature into a compile error in this file.
var _ api.Handler = (*Service)(nil)

// New returns a Service serving the given users.
func New(users ...apitypes.User) *Service {
	return &Service{db: newStore(users)}
}

// Count reports how many users the store holds.
func (s *Service) Count() int { return s.db.count() }

// WithMiddleware wraps every route, outermost first. It is re-exported so a
// main package can add middleware without importing internal/api.
var WithMiddleware = api.WithMiddleware

// NewServer returns the service as an http.Handler: the generated routing,
// authentication, decoding and validation in front, Service behind.
//
// The authenticator is a parameter because api.yaml declares security. A
// server that could be built without one would route requests nobody had
// checked, so the generator does not offer that.
func NewServer(
	s *Service, a api.Authenticator, opts ...api.ServerOption,
) http.Handler {
	return api.NewServer(s, a, opts...)
}

// ErrorTypeBaseURL is the RFC 9457 type prefix this service was generated
// with, re-exported so a caller can build the URI a client will see.
var ErrorTypeBaseURL = api.DefaultErrorOptions().TypeBaseURL

// GetUser returns one user.
//
// The return type is apitypes.User, from the shared module. Identity does not
// declare a User of its own and then convert: there is one User on the
// platform, and this is it.
//
// ctx is the context the authenticator returned, which is how the caller's
// identity gets here. Who the caller is was authentication's job; whether they
// may read this user is this function's, and no specification could state it.
func (s *Service) GetUser(
	ctx context.Context, req api.GetUserRequest,
) (*apitypes.User, error) {
	caller, ok := Caller(ctx)
	if !ok {
		// Unreachable while the spec requires a token, and a 500 rather than a
		// silent read if that ever stops being true.
		return nil, apierr.Internal()
	}
	if caller != req.Path.UserID {
		return nil, apierr.Forbidden("you may only read your own user")
	}

	user, ok := s.db.get(req.Path.UserID)
	if !ok {
		return nil, apierr.NotFound("user", req.Path.UserID)
	}
	return &user, nil
}

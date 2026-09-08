// Package identity implements the identity service.
//
// Everything in this package is hand-written; internal/api is generated from
// api.yaml.
package identity

import (
	"context"
	"net/http"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/identity/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// Service implements the generated handler interface over a store.
type Service struct {
	db *store
}

var _ api.Handler = (*Service)(nil)

// New returns a Service serving the given users.
func New(users ...apitypes.User) *Service {
	return &Service{db: newStore(users)}
}

// WithMiddleware wraps every route, outermost first. It is re-exported so a
// main package can add middleware without importing internal/api.
var WithMiddleware = api.WithMiddleware

// NewServer returns the identity service as an http.Handler. The errors block
// of go-api-gen.yaml is applied by the generated NewServer.
func NewServer(s *Service, opts ...api.ServerOption) http.Handler {
	return api.NewServer(s, opts...)
}

// GetUser returns one user.
//
// req.Path.UserID is a uuid.UUID rather than a string: format: uuid is mapped
// onto uuid.UUID in go-api-gen.yaml, and the generated code has already
// rejected anything that does not parse as one. A path that reaches here
// carries a real UUID, so the only failure left is that nobody has it.
func (s *Service) GetUser(
	_ context.Context, req api.GetUserRequest,
) (*apitypes.User, error) {
	user, ok := s.db.get(req.Path.UserID)
	if !ok {
		return nil, apierr.NotFound("user", req.Path.UserID)
	}
	return &user, nil
}

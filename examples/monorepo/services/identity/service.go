// Package identity implements the identity service.
//
// Everything in this package is hand-written; internal/api is generated from
// api.yaml.
package identity

import (
	"context"
	"net/http"
	"sync"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/identity/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// ErrorTypeBaseURL is prefixed to an error slug to form the RFC 9457 "type"
// URI, and matches errors.type_base_url in go-api-gen.yaml.
const ErrorTypeBaseURL = "https://errors.acme.io/"

// Service is the identity service's implementation of the generated handler
// interface.
type Service struct {
	mu    sync.RWMutex
	users map[uuid.UUID]apitypes.User
}

var _ api.Handler = (*Service)(nil)

// New returns a Service serving the given users.
func New(users ...apitypes.User) *Service {
	s := &Service{users: make(map[uuid.UUID]apitypes.User, len(users))}
	for _, u := range users {
		s.users[u.ID] = u
	}
	return s
}

// NewServer returns the identity service as an http.Handler.
func NewServer(s *Service, opts ...api.ServerOption) http.Handler {
	base := []api.ServerOption{
		api.WithErrorOptions(apierr.Options{TypeBaseURL: ErrorTypeBaseURL}),
	}
	return api.NewServer(s, append(base, opts...)...)
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[req.Path.UserID]
	if !ok {
		return nil, apierr.NotFound("user", req.Path.UserID)
	}
	return &user, nil
}

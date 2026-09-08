// Command identity serves the platform's users.
//
// It is its own Go module. The types it serves come from
// github.com/example/microservices/schema, a module it requires and does not
// own; internal/api is generated from api.yaml.
package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/example/microservices/identity/internal/api"
	"github.com/example/microservices/schema/apitypes"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// service is identity's implementation of the generated handler interface.
type service struct {
	mu    sync.RWMutex
	users map[string]apitypes.User
}

// The generated interface is the contract. Asserting it here turns a spec
// change that alters a signature into a compile error in this file.
var _ api.Handler = (*service)(nil)

func newService(users ...apitypes.User) *service {
	s := &service{users: make(map[string]apitypes.User, len(users))}
	for _, u := range users {
		s.users[u.ID] = u
	}
	return s
}

// GetUser returns one user.
//
// The return type is apitypes.User, from the shared module. Identity does not
// declare a User of its own and then convert: there is one User on the
// platform, and this is it.
func (s *service) GetUser(
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

func newServer(s *service) http.Handler { return api.NewServer(s) }

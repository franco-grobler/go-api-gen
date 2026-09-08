// Package petstore implements the Swagger Petstore.
//
// Everything in this package is hand-written; internal/api is generated from
// api.yaml. This is the single-service case: one spec, one generated package,
// no shared schema root, which is the smallest workspace go-api-gen accepts.
// See ../monorepo and ../microservices for what a shared root exists to solve.
package petstore

import (
	"context"
	"net/http"

	"github.com/example/petstore/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// defaultLimit is the page size for a client that did not ask for one. The
// spec caps limit at 100 and the generated code enforces that; substituting a
// value nobody sent is the handler's job.
const defaultLimit = 20

// Service implements the generated handler interface over a store.
type Service struct {
	db *store
}

// The generated interface is the contract. Asserting it here turns a spec
// change that alters a signature into a compile error in this file.
var _ api.Handler = (*Service)(nil)

// New returns a Service backed by an empty store.
func New() *Service {
	return &Service{db: newStore()}
}

// Count returns how many pets the store holds, for tests and for a readiness
// probe that wants to say something more than "up".
func (s *Service) Count() int { return s.db.count() }

// ListPets returns a page of pets.
//
// Limit is an Optional[int32] because the spec does not require it. Its 1..100
// range has already been enforced, so the only thing left to decide is what an
// absent value means.
func (s *Service) ListPets(
	_ context.Context, req api.ListPetsRequest,
) (*api.Pets, error) {
	pets := s.db.list(int(req.Query.Limit.Or(defaultLimit)))
	return &pets, nil
}

// CreatePets adds a pet and assigns it an id.
//
// The request body is a NewPet rather than a Pet: a client does not choose the
// id, and saying so in the schema is what stops one from arriving. Name has
// already been checked for length; whether a name is free is the store's
// business, and no schema could state it.
func (s *Service) CreatePets(
	_ context.Context, req api.CreatePetsRequest,
) (*api.Pet, error) {
	pet, ok := s.db.add(api.Pet{Name: req.Body.Name, Tag: req.Body.Tag})
	if !ok {
		return nil, apierr.Conflict(
			"a pet called %q is already here", req.Body.Name)
	}
	return &pet, nil
}

// ShowPetByID returns one pet.
//
// req.Path.PetID is an int64, not a string: format: int64 on the parameter is
// what makes the generated code parse it, so a path that reaches here carries
// a number and the only failure left is that no pet has it.
func (s *Service) ShowPetByID(
	_ context.Context, req api.ShowPetByIDRequest,
) (*api.Pet, error) {
	pet, ok := s.db.get(req.Path.PetID)
	if !ok {
		return nil, apierr.NotFound("pet", req.Path.PetID)
	}
	return &pet, nil
}

// WithMiddleware wraps every route, outermost first. It is re-exported so a
// main package can add middleware without importing internal/api.
var WithMiddleware = api.WithMiddleware

// NewServer returns the service as an http.Handler: the generated routing,
// decoding and validation in front, Service behind.
//
// It exists because internal/api is internal to this module, so no main
// package outside it can construct the server itself.
func NewServer(s *Service, opts ...api.ServerOption) http.Handler {
	return api.NewServer(s, opts...)
}

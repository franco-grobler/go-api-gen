// Package petstore implements the Swagger Petstore.
//
// Everything in this package is hand-written; internal/api is generated from
// api.yaml. This is the single-service case: one spec, one generated package,
// no shared schema root, which is the smallest workspace go-api-gen accepts.
// See ../monorepo for the arrangement a shared root exists to solve.
package petstore

import (
	"context"
	"net/http"
	"sync"

	"github.com/example/petstore/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// defaultLimit is the page size for a client that did not ask for one. The
// spec caps limit at 100 and the generated code enforces that; substituting a
// value nobody sent is the handler's job.
const defaultLimit = 20

// Store is an in-memory pet store, and the implementation of the generated
// handler interface.
type Store struct {
	mu     sync.RWMutex
	nextID int64
	pets   []api.Pet
}

// The generated interface is the contract. Asserting it here turns a spec
// change that alters a signature into a compile error in this file.
var _ api.Handler = (*Store)(nil)

// New returns an empty Store.
func New() *Store { return &Store{} }

// Pets returns every pet in the store, in insertion order.
func (s *Store) Pets() []api.Pet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]api.Pet(nil), s.pets...)
}

// NewServer returns the store as an http.Handler: the generated routing,
// decoding and validation in front, Store behind.
//
// It exists because internal/api is internal to this module, so no main
// package outside it can construct the server itself.
func NewServer(s *Store, opts ...api.ServerOption) http.Handler {
	return api.NewServer(s, opts...)
}

// ListPets returns a page of pets.
//
// Limit is an Optional[int32] because the spec does not require it. Its 1..100
// range has already been enforced, so the only thing left to decide is what an
// absent value means.
func (s *Store) ListPets(
	_ context.Context, req api.ListPetsRequest,
) (*api.Pets, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := int(req.Query.Limit.Or(defaultLimit))
	pets := append(api.Pets(nil), s.pets...)
	if len(pets) > limit {
		pets = pets[:limit]
	}
	return &pets, nil
}

// CreatePets adds a pet and assigns it an id.
//
// The request body is a NewPet rather than a Pet: a client does not choose the
// id, and saying so in the schema is what stops one from arriving. Name has
// already been checked for length; whether a name is free is this store's
// business, and no schema could state it.
func (s *Store) CreatePets(
	_ context.Context, req api.CreatePetsRequest,
) (*api.Pet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, pet := range s.pets {
		if pet.Name == req.Body.Name {
			return nil, apierr.Conflict(
				"a pet called %q is already here", pet.Name)
		}
	}

	s.nextID++
	pet := api.Pet{ID: s.nextID, Name: req.Body.Name, Tag: req.Body.Tag}
	s.pets = append(s.pets, pet)
	return &pet, nil
}

// ShowPetByID returns one pet.
//
// req.Path.PetID is an int64, not a string: format: int64 on the parameter is
// what makes the generated code parse it, so a path that reaches here carries
// a number and the only failure left is that no pet has it.
func (s *Store) ShowPetByID(
	_ context.Context, req api.ShowPetByIDRequest,
) (*api.Pet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, pet := range s.pets {
		if pet.ID == req.Path.PetID {
			return &pet, nil
		}
	}
	return nil, apierr.NotFound("pet", req.Path.PetID)
}

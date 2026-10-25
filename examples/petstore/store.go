package petstore

import (
	"sort"
	"sync"

	"github.com/example/petstore/internal/api"
)

// store is the pet store's database.
//
// A map behind a mutex, which is all an example needs. Swapping it for a
// repository over SQL would rewrite this file and nothing else: the handlers
// in service.go never touch the map, only the methods below.
type store struct {
	mu     sync.RWMutex
	nextID int64
	pets   map[int64]api.Pet
}

func newStore() *store {
	return &store{pets: map[int64]api.Pet{}}
}

// add inserts a pet under a freshly assigned id. It reports false when the
// name is taken, which is the one rule the schema cannot state.
func (s *store) add(pet api.Pet) (api.Pet, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.pets {
		if existing.Name == pet.Name {
			return api.Pet{}, false
		}
	}
	s.nextID++
	pet.ID = s.nextID
	s.pets[pet.ID] = pet
	return pet, true
}

func (s *store) get(id int64) (api.Pet, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pet, ok := s.pets[id]
	return pet, ok
}

// list returns at most limit pets, oldest first.
//
// Ids are assigned in insertion order, so sorting by id restores the order a
// map does not keep. A real store would let the database do this.
func (s *store) list(limit int) api.Pets {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]int64, 0, len(s.pets))
	for id := range s.pets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > limit {
		ids = ids[:limit]
	}

	out := make(api.Pets, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.pets[id])
	}
	return out
}

func (s *store) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.pets)
}

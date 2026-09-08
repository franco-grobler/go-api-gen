package identity

import (
	"sync"

	"github.com/example/microservices/schema/apitypes"
)

// store is identity's database: a map behind a mutex, which is all an example
// needs. Swapping it for a repository over SQL would rewrite this file and
// nothing else, since the handlers never touch the map.
//
// The value is apitypes.User, from the shared module. Identity does not keep a
// user of its own and convert on the way out.
type store struct {
	mu    sync.RWMutex
	users map[string]apitypes.User
}

func newStore(users []apitypes.User) *store {
	s := &store{users: make(map[string]apitypes.User, len(users))}
	for _, u := range users {
		s.users[u.ID] = u
	}
	return s
}

func (s *store) get(id string) (apitypes.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[id]
	return user, ok
}

func (s *store) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

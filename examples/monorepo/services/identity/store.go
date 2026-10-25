package identity

import (
	"sync"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
)

// store is identity's database: a map behind a mutex. Swapping it for a
// repository over SQL would rewrite this file and nothing else.
type store struct {
	mu    sync.RWMutex
	users map[uuid.UUID]apitypes.User
}

func newStore(users []apitypes.User) *store {
	s := &store{users: make(map[uuid.UUID]apitypes.User, len(users))}
	for _, u := range users {
		s.users[u.ID] = u
	}
	return s
}

func (s *store) get(id uuid.UUID) (apitypes.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[id]
	return user, ok
}

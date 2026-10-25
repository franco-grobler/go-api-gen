package billing

import (
	"fmt"
	"sync"

	"github.com/example/microservices/billing/internal/api"
)

// store is billing's database.
//
// A map behind a mutex, which is all an example needs. Swapping it for a
// repository over SQL would rewrite this file and nothing else, since the
// handlers never touch the map.
type store struct {
	mu       sync.RWMutex
	nextID   int
	invoices map[string]api.Invoice

	// order is the index a map does not keep. A real store would order by a
	// created_at column instead of carrying one of these.
	order []string
}

func newStore() *store {
	return &store{invoices: map[string]api.Invoice{}}
}

// add assigns an id and inserts the invoice.
func (s *store) add(invoice api.Invoice) api.Invoice {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	invoice.ID = fmt.Sprintf("inv_%03d", s.nextID)
	s.invoices[invoice.ID] = invoice
	s.order = append(s.order, invoice.ID)
	return invoice
}

func (s *store) get(id string) (api.Invoice, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	invoice, ok := s.invoices[id]
	return invoice, ok
}

// all returns every invoice, oldest first.
func (s *store) all() []api.Invoice {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]api.Invoice, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.invoices[id])
	}
	return out
}

func (s *store) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.order)
}

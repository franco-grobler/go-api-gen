package billing

import (
	"sync"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/billing/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/optional"
)

// store is billing's database.
//
// Two maps behind a mutex, which is all an example needs. Swapping them for a
// repository over SQL would rewrite this file and nothing else: the handlers
// in service.go never touch a map, only the methods below.
type store struct {
	mu       sync.RWMutex
	invoices map[uuid.UUID]api.Invoice

	// order is the index a map does not keep. Invoice ids are random, so
	// there is nothing to sort by; a real store would order by a created_at
	// column and page on that instead.
	order []uuid.UUID

	// payers is billing's view of who may be invoiced. The value is
	// apitypes.User: the same Go type the identity service serves, not a
	// structurally identical copy of it.
	payers map[uuid.UUID]apitypes.User
}

func newStore(payers []apitypes.User) *store {
	s := &store{
		invoices: map[uuid.UUID]api.Invoice{},
		payers:   make(map[uuid.UUID]apitypes.User, len(payers)),
	}
	for _, p := range payers {
		s.payers[p.ID] = p
	}
	return s
}

func (s *store) payer(id uuid.UUID) (apitypes.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	payer, ok := s.payers[id]
	return payer, ok
}

func (s *store) add(invoice api.Invoice) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.invoices[invoice.ID] = invoice
	s.order = append(s.order, invoice.ID)
}

// void marks an invoice void, recording the reason when there is one. It
// reports false when no invoice has the id.
func (s *store) void(
	id uuid.UUID, reason optional.Optional[string],
) (api.Invoice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	invoice, ok := s.invoices[id]
	if !ok {
		return api.Invoice{}, false
	}
	invoice.Status = api.InvoiceStatusVoid
	invoice.VoidReason = reason
	s.invoices[id] = invoice
	return invoice, true
}

func (s *store) all() []api.Invoice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.page(0, len(s.order))
}

// after returns up to limit invoices following the given cursor, and the
// cursor for the page after that.
func (s *store) after(cursor string, limit int) ([]api.Invoice, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	from := 0
	if cursor != "" {
		for i, id := range s.order {
			if id.String() == cursor {
				from = i + 1
				break
			}
		}
	}

	items := s.page(from, limit)
	next := ""
	if from+len(items) < len(s.order) && len(items) > 0 {
		next = items[len(items)-1].ID.String()
	}
	return items, next
}

// page reads a window of the index. The caller holds the lock.
func (s *store) page(from, limit int) []api.Invoice {
	if from > len(s.order) {
		from = len(s.order)
	}
	ids := s.order[from:]
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]api.Invoice, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.invoices[id])
	}
	return out
}

func (s *store) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.order)
}

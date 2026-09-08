// Command billing raises invoices against the platform's users.
//
// It is its own Go module, built and deployed without identity. What the two
// share is github.com/example/microservices/schema: one module, required by
// both, owned by neither. internal/api is generated from api.yaml.
package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/example/microservices/billing/internal/api"
	"github.com/example/microservices/schema/apitypes"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// service is billing's implementation of the generated handler interface.
type service struct {
	mu       sync.Mutex
	nextID   int
	invoices []api.Invoice
}

var _ api.Handler = (*service)(nil)

func newService() *service { return &service{} }

// ListInvoices returns every invoice raised so far.
func (s *service) ListInvoices(
	_ context.Context, _ api.ListInvoicesRequest,
) (*[]api.Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := append([]api.Invoice(nil), s.invoices...)
	return &out, nil
}

// CreateInvoice raises an invoice.
//
// req.Body.Payer is an apitypes.User that arrived as JSON and was decoded,
// validated, and handed over as the same Go type identity would have returned
// from its own handler. Nothing in this module converts between the two,
// because there are not two.
func (s *service) CreateInvoice(
	_ context.Context, req api.CreateInvoiceRequest,
) (*api.Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Body.Payer.Status != apitypes.UserStatusActive {
		// A rule about the shared type, enforced by the service that cares.
		// The schema says what a User is; whether this one may be invoiced is
		// billing's business.
		return nil, apierr.Validation().
			Field("body", "payer", "status").
			Fail("payer_not_active", "This payer is not active")
	}

	s.nextID++
	invoice := api.Invoice{
		ID:    fmt.Sprintf("inv_%03d", s.nextID),
		Payer: req.Body.Payer,
		Total: req.Body.Total,
	}
	s.invoices = append(s.invoices, invoice)
	return &invoice, nil
}

func newServer(s *service) http.Handler { return api.NewServer(s) }

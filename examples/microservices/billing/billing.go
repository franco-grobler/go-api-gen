// Package billing raises invoices against the platform's users.
//
// It is its own Go module, built and deployed without identity. What the two
// share is github.com/example/microservices/schema: one module, required by
// both, owned by neither. internal/api is generated from api.yaml.
package billing

import (
	"context"
	"net/http"

	"github.com/example/microservices/billing/internal/api"
	"github.com/example/microservices/schema/apitypes"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// Service implements the generated handler interface over a store.
type Service struct {
	db *store
}

var _ api.Handler = (*Service)(nil)

// New returns a Service backed by an empty store.
func New() *Service {
	return &Service{db: newStore()}
}

// Count reports how many invoices the store holds.
func (s *Service) Count() int { return s.db.count() }

// WithMiddleware wraps every route, outermost first. It is re-exported so a
// main package can add middleware without importing internal/api.
var WithMiddleware = api.WithMiddleware

// NewServer returns the service as an http.Handler: the generated routing,
// decoding and validation in front, Service behind.
func NewServer(s *Service, opts ...api.ServerOption) http.Handler {
	return api.NewServer(s, opts...)
}

// ErrorTypeBaseURL is the RFC 9457 type prefix this service was generated
// with, re-exported so a caller can build the URI a client will see.
var ErrorTypeBaseURL = api.DefaultErrorOptions().TypeBaseURL

// ListInvoices returns every invoice raised so far.
func (s *Service) ListInvoices(
	_ context.Context, _ api.ListInvoicesRequest,
) (*[]api.Invoice, error) {
	out := s.db.all()
	return &out, nil
}

// CreateInvoice raises an invoice.
//
// req.Body.Payer is an apitypes.User that arrived as JSON and was decoded,
// validated, and handed over as the same Go type identity would have returned
// from its own handler. Nothing in this module converts between the two,
// because there are not two.
func (s *Service) CreateInvoice(
	_ context.Context, req api.CreateInvoiceRequest,
) (*api.Invoice, error) {
	if req.Body.Payer.Status != apitypes.UserStatusActive {
		// A rule about the shared type that the shared schema cannot state.
		// user.yaml says what a User is; whether this one may be invoiced is
		// billing's business, and identity would answer differently.
		return nil, apierr.Validation().
			Field("body", "payer", "status").
			Fail("payer_not_active", "This payer is not active")
	}

	invoice := s.db.add(api.Invoice{
		Payer: req.Body.Payer,
		Total: req.Body.Total,
	})
	return &invoice, nil
}

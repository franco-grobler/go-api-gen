// Package billing implements the billing service.
//
// Everything in this package is hand-written; internal/api is generated from
// api.yaml. By the time a method on Service runs, the request has been routed,
// decoded, and checked against the specification, so what is left here is the
// part a schema cannot state: state.
package billing

import (
	"context"
	"net/http"
	"sync"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/billing/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
	"github.com/franco-grobler/go-api-gen/runtime/optional"
)

// ErrorTypeBaseURL is prefixed to an error slug to form the RFC 9457 "type"
// URI. It matches errors.type_base_url in go-api-gen.yaml, which describes the
// same value to a reader of the documentation but does not yet reach generated
// code, so a binary passes it at construction.
const ErrorTypeBaseURL = "https://errors.acme.io/"

// defaultLimit is the page size for a client that did not ask for one. The
// spec declares the same default on the limit parameter; the generated code
// enforces the 1..100 range but does not substitute a missing value, so the
// handler applies it.
const defaultLimit = 25

// Service is the billing service's implementation of the generated handler
// interface.
type Service struct {
	mu       sync.Mutex
	invoices []api.Invoice

	// payers is billing's view of who may be invoiced. The value is
	// apitypes.User: the same Go type the identity service serves, not a
	// structurally identical copy of it.
	payers map[uuid.UUID]apitypes.User
}

// The generated interface is the contract. Asserting it here turns a spec
// change that alters a signature into a compile error in this file, rather
// than a route that quietly stops being served.
var _ api.Handler = (*Service)(nil)

// New returns a Service that will invoice the given payers.
func New(payers ...apitypes.User) *Service {
	s := &Service{payers: make(map[uuid.UUID]apitypes.User, len(payers))}
	for _, p := range payers {
		s.payers[p.ID] = p
	}
	return s
}

// NewServer returns the billing service as an http.Handler: the generated
// routing, decoding and validation in front, Service behind.
func NewServer(s *Service, opts ...api.ServerOption) http.Handler {
	base := []api.ServerOption{
		api.WithErrorOptions(apierr.Options{TypeBaseURL: ErrorTypeBaseURL}),
	}
	return api.NewServer(s, append(base, opts...)...)
}

// Invoices returns every invoice created so far, oldest first.
func (s *Service) Invoices() []api.Invoice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.Invoice(nil), s.invoices...)
}

// ListInvoices returns one page of invoices.
//
// req.Query.Limit is an Optional[int] rather than an int, so "the client did
// not ask" stays distinguishable from "the client asked for zero" -- though a
// zero cannot reach here at all, since the spec sets minimum: 1 and the
// generated code rejected it first.
func (s *Service) ListInvoices(
	_ context.Context, req api.ListInvoicesRequest,
) (*api.ListInvoicesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	from := 0
	if cursor, ok := req.Query.Cursor.Get(); ok {
		// The cursor is opaque to the client, not to us: it is the ID of the
		// last invoice on the page before this one.
		for i, inv := range s.invoices {
			if inv.ID.String() == cursor {
				from = i + 1
				break
			}
		}
	}

	page := apitypes.PageInfo{Total: len(s.invoices)}
	items := append([]api.Invoice(nil), s.invoices[from:]...)
	if limit := req.Query.Limit.Or(defaultLimit); len(items) > limit {
		items = items[:limit]
		page.NextCursor = optional.Some(items[limit-1].ID.String())
	}
	return &api.ListInvoicesResponse{Items: items, Page: page}, nil
}

// CreateInvoice records a new invoice.
//
// req.Body arrives decoded and checked: PayerID parsed as a UUID, Total
// present, its currency exactly three characters. The one check left is the
// one no schema can express, because it depends on data this service holds.
func (s *Service) CreateInvoice(
	_ context.Context, req api.CreateInvoiceRequest,
) (*api.Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	payer, ok := s.payers[req.Body.PayerID]
	if !ok {
		// The same builder the generated validation uses, so a rule that
		// needed a lookup is indistinguishable, in the response, from one the
		// schema stated outright.
		return nil, apierr.Validation().
			Field("body", "payer_id").
			Fail("payer_unknown", "No payer with this ID")
	}

	invoice := api.Invoice{
		ID:     uuid.New(),
		Payer:  payer,
		Total:  req.Body.Total,
		Status: api.InvoiceStatusDraft,
	}
	s.invoices = append(s.invoices, invoice)
	return &invoice, nil
}

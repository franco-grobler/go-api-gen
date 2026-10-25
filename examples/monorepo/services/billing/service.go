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

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/billing/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
	"github.com/franco-grobler/go-api-gen/runtime/optional"
)

// defaultLimit is the page size for a client that did not ask for one. The
// spec declares the same default on the limit parameter; the generated code
// enforces the 1..100 range but does not substitute a missing value, so the
// handler applies it.
const defaultLimit = 25

// Service implements the generated handler interface over a store.
type Service struct {
	db *store
}

// The generated interface is the contract. Asserting it here turns a spec
// change that alters a signature into a compile error in this file.
var _ api.Handler = (*Service)(nil)

// New returns a Service that will invoice the given payers.
func New(payers ...apitypes.User) *Service {
	return &Service{db: newStore(payers)}
}

// WithMiddleware wraps every route, outermost first. It is re-exported so a
// main package can add middleware without importing internal/api.
var WithMiddleware = api.WithMiddleware

// NewServer returns the billing service as an http.Handler: the generated
// routing, decoding and validation in front, Service behind.
//
// It exists because internal/api is internal to this service, so no main
// package can reach it. The errors block of go-api-gen.yaml needs no repeating
// here; the generated NewServer has already applied it.
func NewServer(s *Service, opts ...api.ServerOption) http.Handler {
	return api.NewServer(s, opts...)
}

// Invoices returns every invoice created so far, oldest first.
func (s *Service) Invoices() []api.Invoice { return s.db.all() }

// ListInvoices returns one page of invoices.
//
// req.Query.Limit is an Optional[int] rather than an int, so "the client did
// not ask" stays distinguishable from "the client asked for zero" -- though a
// zero cannot reach here at all, since the spec sets minimum: 1 and the
// generated code rejected it first.
func (s *Service) ListInvoices(
	_ context.Context, req api.ListInvoicesRequest,
) (*api.ListInvoicesResponse, error) {
	items, next := s.db.after(
		req.Query.Cursor.Or(""), req.Query.Limit.Or(defaultLimit))

	page := apitypes.PageInfo{Total: s.db.count()}
	if next != "" {
		page.NextCursor = optional.Some(next)
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
	payer, ok := s.db.payer(req.Body.PayerID)
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
	s.db.add(invoice)
	return &invoice, nil
}

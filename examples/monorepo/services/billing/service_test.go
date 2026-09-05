package billing_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/billing/internal/api"
	"github.com/franco-grobler/go-api-gen/runtime/apierr"
	"github.com/google/uuid"
)

// handler is a hand-written implementation of the generated interface. It does
// no parsing and no validation: by the time a method runs, the request has
// already been decoded and checked.
type handler struct {
	created *api.CreateInvoice
	taken   map[string]bool
}

func (h *handler) ListInvoices(
	ctx contextType, req api.ListInvoicesRequest,
) (*api.ListInvoicesResponse, error) {
	return &api.ListInvoicesResponse{
		Items: []api.Invoice{},
		Page:  apitypes.PageInfo{Total: 0},
	}, nil
}

func (h *handler) CreateInvoice(
	ctx contextType, req api.CreateInvoiceRequest,
) (*api.Invoice, error) {
	// A check that needs state lives here, and uses the same error builder as
	// the generated validation, so the response shape is identical.
	if h.taken[req.Body.PayerID.String()] {
		return nil, apierr.Validation().
			Field("body", "payer_id").
			Fail("payer_blocked", "this payer is blocked")
	}
	h.created = &req.Body
	return &api.Invoice{
		ID:     uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Payer:  apitypes.User{},
		Total:  req.Body.Total,
		Status: api.InvoiceStatusDraft,
	}, nil
}

func post(t *testing.T, h http.Handler, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/invoices", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	res := rec.Result()
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var decoded map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("response is not JSON: %v\n%s", err, raw)
		}
	}
	return res.StatusCode, decoded
}

func TestValidRequestReachesTheHandler(t *testing.T) {
	h := &handler{}
	srv := api.NewServer(h)

	status, body := post(t, srv, `{
		"payer_id": "22222222-2222-2222-2222-222222222222",
		"total": {"amount_minor": 1500, "currency": "GBP"}
	}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %v", status, body)
	}
	if h.created == nil {
		t.Fatal("the handler was never called")
	}
	if got := h.created.Total.Currency; got != "GBP" {
		t.Errorf("decoded currency = %q, want GBP", got)
	}
	if body["status"] != "draft" {
		t.Errorf("response status = %v, want draft", body["status"])
	}
}

// Constraint failures never reach the handler and are reported together.
func TestSchemaViolationsAreReportedTogether(t *testing.T) {
	h := &handler{}
	status, body := post(t, api.NewServer(h), `{
		"payer_id": "22222222-2222-2222-2222-222222222222",
		"total": {"amount_minor": 1500, "currency": "TOOLONG"}
	}`)

	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %v", status, body)
	}
	if h.created != nil {
		t.Error("an invalid request reached the handler")
	}

	errs, _ := body["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", body["errors"])
	}
	e, _ := errs[0].(map[string]any)
	if e["type"] != "string_too_long" {
		t.Errorf("type = %v, want string_too_long", e["type"])
	}
	loc, _ := e["loc"].([]any)
	if len(loc) != 3 || loc[0] != "body" || loc[1] != "total" ||
		loc[2] != "currency" {
		t.Errorf("loc = %v, want [body total currency]", e["loc"])
	}
}

// A required field the client omitted must be reported as missing, not
// silently accepted as the zero value.
func TestMissingRequiredFieldIsReported(t *testing.T) {
	status, body := post(t, api.NewServer(&handler{}), `{}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %v", status, body)
	}

	var missing []string
	errs, _ := body["errors"].([]any)
	for _, raw := range errs {
		e, _ := raw.(map[string]any)
		if e["type"] != "missing" {
			continue
		}
		loc, _ := e["loc"].([]any)
		missing = append(missing, loc[len(loc)-1].(string))
	}
	want := map[string]bool{"payer_id": true, "total": true}
	for _, name := range missing {
		delete(want, name)
	}
	if len(want) > 0 {
		t.Errorf("missing fields reported = %v, did not report %v",
			missing, want)
	}
}

// A zero value the client actually sent is not "missing".
func TestExplicitZeroIsNotMissing(t *testing.T) {
	status, body := post(t, api.NewServer(&handler{}), `{
		"payer_id": "22222222-2222-2222-2222-222222222222",
		"total": {"amount_minor": 0, "currency": "GBP"}
	}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; an explicit 0 was "+
			"treated as absent: %v", status, body)
	}
}

func TestMalformedJSON(t *testing.T) {
	status, body := post(t, api.NewServer(&handler{}), `{not json`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %v", status, body)
	}
	errs, _ := body["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", body["errors"])
	}
	if e, _ := errs[0].(map[string]any); e["type"] != "json_invalid" {
		t.Errorf("type = %v, want json_invalid", e["type"])
	}
}

// A handler-raised failure comes back in exactly the same envelope as a
// generated one.
func TestHandlerRaisedErrorMatchesSchemaShape(t *testing.T) {
	blocked := "22222222-2222-2222-2222-222222222222"
	h := &handler{taken: map[string]bool{blocked: true}}

	status, body := post(t, api.NewServer(h), `{
		"payer_id": "`+blocked+`",
		"total": {"amount_minor": 100, "currency": "GBP"}
	}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %v", status, body)
	}
	errs, _ := body["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", body["errors"])
	}
	e, _ := errs[0].(map[string]any)
	if e["type"] != "payer_blocked" {
		t.Errorf("type = %v, want payer_blocked", e["type"])
	}
	loc, _ := e["loc"].([]any)
	if len(loc) != 2 || loc[0] != "body" || loc[1] != "payer_id" {
		t.Errorf("loc = %v, want [body payer_id]", e["loc"])
	}
}

func TestRouting(t *testing.T) {
	srv := api.NewServer(&handler{})

	r := httptest.NewRequest(http.MethodGet, "/invoices", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /invoices = %d, want 200: %s", rec.Code, rec.Body)
	}

	// An unregistered path is the mux's business, not ours.
	r = httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", rec.Code)
	}
}

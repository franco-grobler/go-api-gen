package billing_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/billing"
)

const payerID = "22222222-2222-2222-2222-222222222222"

// payer is seeded into every Service under test. Its email is on the acme.com
// domain, which is what the corporate_domain rule in gen/apitypes requires.
func payer() apitypes.User {
	return apitypes.User{
		ID:     uuid.MustParse(payerID),
		Email:  "ada@acme.com",
		Status: apitypes.UserStatusActive,
	}
}

// serve returns the real service behind the generated server, which is the
// arrangement a deployment uses. Nothing here is a test double: the point of
// these tests is what the generated layer does before Service is reached.
func serve(t *testing.T) (*billing.Service, http.Handler) {
	t.Helper()
	svc := billing.New(payer())
	return svc, billing.NewServer(svc)
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
	svc, srv := serve(t)

	status, body := post(t, srv, `{
		"payer_id": "`+payerID+`",
		"total": {"amount_minor": 1500, "currency": "GBP"}
	}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %v", status, body)
	}

	created := svc.Invoices()
	if len(created) != 1 {
		t.Fatalf("the service holds %d invoices, want 1", len(created))
	}
	if got := created[0].Total.Currency; got != "GBP" {
		t.Errorf("decoded currency = %q, want GBP", got)
	}
	if body["status"] != "draft" {
		t.Errorf("response status = %v, want draft", body["status"])
	}
}

// Constraint failures never reach the handler and are reported together.
func TestSchemaViolationsAreReportedTogether(t *testing.T) {
	svc, srv := serve(t)
	status, body := post(t, srv, `{
		"payer_id": "`+payerID+`",
		"total": {"amount_minor": 1500, "currency": "TOOLONG"}
	}`)

	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %v", status, body)
	}
	if got := len(svc.Invoices()); got != 0 {
		t.Errorf("an invalid request reached the handler: %d invoices", got)
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
	_, srv := serve(t)
	status, body := post(t, srv, `{}`)
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
	_, srv := serve(t)
	status, body := post(t, srv, `{
		"payer_id": "`+payerID+`",
		"total": {"amount_minor": 0, "currency": "GBP"}
	}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; an explicit 0 was "+
			"treated as absent: %v", status, body)
	}
}

func TestMalformedJSON(t *testing.T) {
	_, srv := serve(t)
	status, body := post(t, srv, `{not json`)
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

// A check the handler had to make itself, because it needed state, comes back
// in exactly the same envelope as one the schema stated outright.
func TestHandlerRaisedErrorMatchesSchemaShape(t *testing.T) {
	_, srv := serve(t)
	status, body := post(t, srv, `{
		"payer_id": "33333333-3333-3333-3333-333333333333",
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
	if e["type"] != "payer_unknown" {
		t.Errorf("type = %v, want payer_unknown", e["type"])
	}
	loc, _ := e["loc"].([]any)
	if len(loc) != 2 || loc[0] != "body" || loc[1] != "payer_id" {
		t.Errorf("loc = %v, want [body payer_id]", e["loc"])
	}
}

// NewServer passes ErrorTypeBaseURL through to the middleware, so the problem
// type is a resolvable URI rather than a bare slug.
func TestProblemTypeCarriesTheBaseURL(t *testing.T) {
	_, srv := serve(t)
	_, body := post(t, srv, `{}`)
	want := billing.ErrorTypeBaseURL + "validation_failed"
	if body["type"] != want {
		t.Errorf("type = %v, want %q", body["type"], want)
	}
}

// Pagination is the handler's own work: the spec constrains limit, but the
// generated code does not substitute the default for a client that omits it.
func TestListPaginates(t *testing.T) {
	svc, srv := serve(t)
	for range 3 {
		status, body := post(t, srv, `{
			"payer_id": "`+payerID+`",
			"total": {"amount_minor": 100, "currency": "GBP"}
		}`)
		if status != http.StatusCreated {
			t.Fatalf("seeding failed with %d: %v", status, body)
		}
	}

	first := get(t, srv, "/invoices?limit=2")
	items, _ := first["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("first page = %d items, want 2", len(items))
	}
	page, _ := first["page"].(map[string]any)
	if page["total"] != float64(3) {
		t.Errorf("page.total = %v, want 3", page["total"])
	}
	cursor, ok := page["next_cursor"].(string)
	if !ok {
		t.Fatalf("page = %v, want a next_cursor", page)
	}
	if want := svc.Invoices()[1].ID.String(); cursor != want {
		t.Errorf("next_cursor = %q, want %q", cursor, want)
	}

	second := get(t, srv, "/invoices?limit=2&cursor="+cursor)
	items, _ = second["items"].([]any)
	if len(items) != 1 {
		t.Errorf("second page = %d items, want the remaining 1", len(items))
	}
}

func TestRouting(t *testing.T) {
	_, srv := serve(t)

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

func get(t *testing.T, h http.Handler, target string) map[string]any {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	res := rec.Result()
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", target, res.StatusCode, raw)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, raw)
	}
	return decoded
}

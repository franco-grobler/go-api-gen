package billing_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/example/microservices/billing"
	"github.com/example/microservices/billing/internal/api"
	"github.com/example/microservices/schema/apitypes"
)

// The field billing generated is the type the shared module declares, not a
// copy with the same shape. A generator that emitted its own User would fail
// to compile this line.
var _ apitypes.User = api.Invoice{}.Payer

func ada() apitypes.User {
	return apitypes.User{
		ID:     "5b0a3f2e-0000-4000-8000-000000000001",
		Email:  "ada@example.com",
		Status: apitypes.UserStatusActive,
	}
}

func post(t *testing.T, h http.Handler, body []byte) (int, []byte) {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/invoices", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	res := rec.Result()
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return res.StatusCode, out
}

// body builds a create request the way a caller would: it has just fetched the
// payer from identity, so what it sends is a marshalled apitypes.User. Billing
// is a different module and decodes it as the same Go type.
func body(t *testing.T, payer apitypes.User, currency string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"payer": payer,
		"total": map[string]any{
			"amount_minor": 1500,
			"currency":     currency,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCreateInvoiceRoundTripsTheSharedUser(t *testing.T) {
	svc := billing.New()
	status, out := post(t, billing.NewServer(svc), body(t, ada(), "GBP"))
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", status, out)
	}

	var invoice api.Invoice
	if err := json.Unmarshal(out, &invoice); err != nil {
		t.Fatalf("response does not decode: %v\n%s", err, out)
	}
	if invoice.Payer.Email != ada().Email {
		t.Errorf("payer = %+v, want Ada", invoice.Payer)
	}
	if invoice.Payer.Status != apitypes.UserStatusActive {
		t.Errorf("status = %q, want the shared constant",
			invoice.Payer.Status)
	}
	if svc.Count() != 1 {
		t.Errorf("the service holds %d invoices, want 1", svc.Count())
	}
}

// The constraints live in the schema module, and billing enforces them without
// declaring any of them: currency is three characters because user.yaml's
// neighbour said so.
func TestSharedConstraintsAreEnforcedHere(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want []string
	}{{
		name: "currency too long",
		body: body(t, ada(), "TOOLONG"),
		want: []string{"string_too_long at body/total/currency"},
	}, {
		name: "payer email too short",
		body: body(t, apitypes.User{
			ID: "u1", Email: "a", Status: apitypes.UserStatusActive,
		}, "GBP"),
		want: []string{"string_too_short at body/payer/email"},
	}, {
		name: "payer status not in the enum",
		body: body(t, apitypes.User{
			ID: "u1", Email: "ada@example.com", Status: "retired",
		}, "GBP"),
		want: []string{"enum at body/payer/status"},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := billing.New()
			status, out := post(t, billing.NewServer(svc), tt.body)
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", status, out)
			}
			if got := failures(t, out); strings.Join(got, ", ") !=
				strings.Join(tt.want, ", ") {
				t.Errorf("errors = %v, want %v", got, tt.want)
			}
			if svc.Count() != 0 {
				t.Error("an invalid request reached the handler")
			}
		})
	}
}

// A rule about the shared type that the shared schema cannot state: billing
// decides who it will invoice.
func TestInactivePayerIsRejected(t *testing.T) {
	payer := ada()
	payer.Status = apitypes.UserStatusSuspended

	status, out := post(t, billing.NewServer(billing.New()), body(t, payer, "GBP"))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", status, out)
	}
	want := []string{"payer_not_active at body/payer/status"}
	if got := failures(t, out); strings.Join(got, ", ") !=
		strings.Join(want, ", ") {
		t.Errorf("errors = %v, want %v", got, want)
	}
}

// The missing-field rule holds across the module boundary: an absent required
// object is reported once, not walked as a zero value.
func TestMissingFieldsAreReportedOnce(t *testing.T) {
	status, out := post(t, billing.NewServer(billing.New()), []byte(`{}`))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", status, out)
	}
	want := []string{"missing at body/payer", "missing at body/total"}
	got := failures(t, out)
	sort.Strings(got)
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("errors = %v, want %v", got, want)
	}
}

func TestListInvoices(t *testing.T) {
	svc := billing.New()
	srv := billing.NewServer(svc)
	if status, out := post(t, srv, body(t, ada(), "GBP")); status !=
		http.StatusCreated {
		t.Fatalf("seeding failed with %d: %s", status, out)
	}

	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/invoices", http.NoBody)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	var invoices []api.Invoice
	if err := json.Unmarshal(rec.Body.Bytes(), &invoices); err != nil {
		t.Fatal(err)
	}
	if len(invoices) != 1 || invoices[0].Payer.ID != ada().ID {
		t.Errorf("invoices = %+v, want one for Ada", invoices)
	}
}

// failures renders the errors array as "slug at a/b" strings, so a mismatch
// reports what actually came back rather than a length.
func failures(t *testing.T, out []byte) []string {
	t.Helper()
	var problem map[string]any
	if err := json.Unmarshal(out, &problem); err != nil {
		t.Fatalf("response is not a problem document: %v\n%s", err, out)
	}
	entries, _ := problem["errors"].([]any)
	got := make([]string, 0, len(entries))
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		loc, _ := e["loc"].([]any)
		parts := make([]string, len(loc))
		for i, seg := range loc {
			parts[i] = fmt.Sprint(seg)
		}
		got = append(got, fmt.Sprintf("%v at %s",
			e["type"], strings.Join(parts, "/")))
	}
	return got
}

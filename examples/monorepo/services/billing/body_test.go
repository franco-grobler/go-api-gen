package billing_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/acme/monorepo/services/billing"
)

// These go over a real connection rather than to a recorder: only an
// http.Server shows how a handler sees a request with no body, which is with
// a Body that is not nil and, when the body was chunked, a length of -1.

// chunked hides a body's length, so the client streams it chunked.
type chunked struct{ io.Reader }

func send(
	t *testing.T, srv *httptest.Server, path string, body io.Reader,
) (int, map[string]any) {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(),
		http.MethodPost, srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := body.(chunked); ok {
		r.ContentLength = -1
	}
	res, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, raw)
	}
	return res.StatusCode, decoded
}

func listen(t *testing.T) (*billing.Service, *httptest.Server) {
	t.Helper()
	svc := billing.New(payer())
	srv := httptest.NewServer(billing.NewServer(svc))
	t.Cleanup(srv.Close)
	return svc, srv
}

// A required body that is not there is missing, at the body itself, however
// the client said so: with a Content-Length of 0, or with a chunked stream
// that ends before it starts. It is not malformed JSON, and the body's own
// fields are not reported missing one by one.
func TestAbsentRequiredBodyIsMissing(t *testing.T) {
	for name, body := range map[string]io.Reader{
		"content-length 0": http.NoBody,
		"empty chunked":    chunked{strings.NewReader("")},
	} {
		t.Run(name, func(t *testing.T) {
			svc, srv := listen(t)
			status, got := send(t, srv, "/invoices", body)
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %v", status, got)
			}
			if f := failures(t, got); strings.Join(f, ", ") != "missing at body" {
				t.Errorf("errors = %v, want [missing at body]", f)
			}
			if n := len(svc.Invoices()); n != 0 {
				t.Errorf("the handler ran: %d invoices", n)
			}
		})
	}
}

// A chunked body that has something in it is decoded as any other.
func TestChunkedBodyIsDecoded(t *testing.T) {
	_, srv := listen(t)
	status, got := send(t, srv, "/invoices", chunked{strings.NewReader(`{
		"payer_id": "` + payerID + `",
		"total": {"amount_minor": 100, "currency": "GBP"}
	}`)})
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %v", status, got)
	}
}

// A body that is there but is not JSON is malformed, whether the body is
// required or not. Whitespace is not JSON, so it is malformed too, not absent.
func TestMalformedBodyIsInvalid(t *testing.T) {
	svc, srv := listen(t)
	id := draft(t, svc, srv)
	for _, path := range []string{"/invoices", "/invoices/" + id + "/void"} {
		for _, body := range []string{"{not json", " \n"} {
			status, got := send(t, srv, path, strings.NewReader(body))
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("%s %q: status = %d, want 422: %v",
					path, body, status, got)
			}
			if f := failures(t, got); strings.Join(f, ", ") != "json_invalid at body" {
				t.Errorf("%s %q: errors = %v, want [json_invalid at body]",
					path, body, f)
			}
		}
	}
}

// An optional body may be left off entirely, chunked or not: the handler
// sees it unset, and voids the invoice without a reason.
func TestOptionalBodyMayBeOmitted(t *testing.T) {
	for name, body := range map[string]io.Reader{
		"content-length 0": http.NoBody,
		"empty chunked":    chunked{strings.NewReader("")},
	} {
		t.Run(name, func(t *testing.T) {
			svc, srv := listen(t)
			id := draft(t, svc, srv)
			status, got := send(t, srv, "/invoices/"+id+"/void", body)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200: %v", status, got)
			}
			if got["status"] != "void" {
				t.Errorf("status = %v, want void", got["status"])
			}
			if _, ok := got["void_reason"]; ok {
				t.Errorf("void_reason = %v, want none", got["void_reason"])
			}
		})
	}
}

// An optional body that is sent is decoded and checked as a required one is.
func TestOptionalBodyIsDecodedAndChecked(t *testing.T) {
	svc, srv := listen(t)
	id := draft(t, svc, srv)

	status, got := send(t, srv, "/invoices/"+id+"/void", strings.NewReader("{}"))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %v", status, got)
	}
	if f := failures(t, got); strings.Join(f, ", ") != "missing at body/reason" {
		t.Errorf("errors = %v, want [missing at body/reason]", f)
	}

	status, got = send(t, srv, "/invoices/"+id+"/void",
		strings.NewReader(`{"reason": "sent twice"}`))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", status, got)
	}
	if got["void_reason"] != "sent twice" {
		t.Errorf("void_reason = %v, want the one sent", got["void_reason"])
	}
}

// draft creates an invoice and returns its id.
func draft(t *testing.T, svc *billing.Service, srv *httptest.Server) string {
	t.Helper()
	status, got := send(t, srv, "/invoices", strings.NewReader(`{
		"payer_id": "`+payerID+`",
		"total": {"amount_minor": 100, "currency": "GBP"}
	}`))
	if status != http.StatusCreated {
		t.Fatalf("creating an invoice: status = %d: %v", status, got)
	}
	invoices := svc.Invoices()
	return invoices[len(invoices)-1].ID.String()
}

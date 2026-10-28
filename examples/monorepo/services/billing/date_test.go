package billing_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/franco-grobler/go-api-gen/runtime/date"
)

// list sends GET target and returns the status and the raw body, whatever
// the status is.
func list(t *testing.T, h http.Handler, target string) (int, string) {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	res := rec.Result()
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// seed raises one invoice for each due date, "" for none.
func seed(t *testing.T, h http.Handler, due ...string) {
	t.Helper()
	for _, d := range due {
		field := ""
		if d != "" {
			field = `, "due_on": "` + d + `"`
		}
		status, body := post(t, h, `{
			"payer_id": "`+payerID+`",
			"total": {"amount_minor": 100, "currency": "GBP"}`+field+`
		}`)
		if status != http.StatusCreated {
			t.Fatalf("seeding %q failed with %d: %v", d, status, body)
		}
	}
}

// A due date is a full-date in the request body, a date.Date in the
// handler, and a full-date again in the response: no time, no zone.
func TestDueDateRoundTrips(t *testing.T) {
	svc, srv := serve(t)
	status, body := post(t, srv, `{
		"payer_id": "`+payerID+`",
		"total": {"amount_minor": 100, "currency": "GBP"},
		"due_on": "2028-02-29"
	}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %v", status, body)
	}
	if body["due_on"] != "2028-02-29" {
		t.Errorf("response due_on = %v, want 2028-02-29", body["due_on"])
	}
	due, ok := svc.Invoices()[0].DueOn.Get()
	if !ok || due != date.MustParse("2028-02-29") {
		t.Errorf("decoded due_on = %v", svc.Invoices()[0].DueOn)
	}
}

// ?due_by=2026-10-27 is a date, the way a person writes one, and it is
// inclusive: an invoice due that day is listed.
func TestListFiltersByDueDate(t *testing.T) {
	_, srv := serve(t)
	seed(t, srv, "2026-10-26", "2026-10-27", "2026-10-28", "")

	status, raw := list(t, srv, "/invoices?due_by=2026-10-27")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, raw)
	}
	var page struct {
		Items []struct {
			DueOn string `json:"due_on"`
		} `json:"items"`
		Page struct {
			Total int `json:"total"`
		} `json:"page"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range page.Items {
		got = append(got, item.DueOn)
	}
	if strings.Join(got, " ") != "2026-10-26 2026-10-27" ||
		page.Page.Total != 2 {
		t.Errorf("listed %v of %d, want the two due by 2026-10-27",
			got, page.Page.Total)
	}

	// Without the filter, every invoice is listed, including the one with
	// no due date, which encodes no due_on at all.
	_, raw = list(t, srv, "/invoices")
	if n := strings.Count(raw, `"due_on"`); n != 3 {
		t.Errorf("want three due_on fields: %s", raw)
	}
}

// A timestamp is not a date, nor is a day the calendar does not have; each
// is a 422 that names the parameter, before the handler runs.
func TestDueByMustBeAFullDate(t *testing.T) {
	_, srv := serve(t)
	for _, sent := range []string{
		"2026-10-27T00:00:00Z",
		"2026-02-29",
		"2026-13-01",
		"27-10-2026",
	} {
		status, raw := list(t, srv, "/invoices?due_by="+sent)
		if status != http.StatusUnprocessableEntity {
			t.Errorf("due_by=%s: status = %d, want 422: %s",
				sent, status, raw)
			continue
		}
		want := `{"type":"date_parsing","loc":["query","due_by"]`
		if !strings.Contains(raw, want) {
			t.Errorf("due_by=%s: want %s in %s", sent, want, raw)
		}
	}

	status, body := post(t, srv, `{
		"payer_id": "`+payerID+`",
		"total": {"amount_minor": 100, "currency": "GBP"},
		"due_on": "2026-10-27T00:00:00Z"
	}`)
	if status != http.StatusUnprocessableEntity {
		t.Errorf("a timestamp due_on: status = %d, want 422: %v",
			status, body)
	}
}

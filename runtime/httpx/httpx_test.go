package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

func quiet() apierr.Options {
	return apierr.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func run(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/x", http.NoBody)
	h.ServeHTTP(rec, req)
	return rec
}

func TestWriteJSON(t *testing.T) {
	rec := run(t, Wrap(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, r, http.StatusCreated, map[string]int{"n": 1})
		}), Config{Errors: quiet()}))

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if got["n"] != 1 {
		t.Errorf("body = %v", got)
	}
}

// Marshalling happens before the status is written, so a failure becomes a
// problem document rather than a truncated 200.
func TestWriteJSONMarshalFailure(t *testing.T) {
	rec := run(t, Wrap(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, r, http.StatusOK, make(chan int))
		}), Config{Errors: quiet()}))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != apierr.ContentType {
		t.Errorf("Content-Type = %q, want %q", ct, apierr.ContentType)
	}
}

func TestWriteStatus(t *testing.T) {
	rec := run(t, Wrap(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			WriteStatus(w, http.StatusNoContent)
		}), Config{Errors: quiet()}))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body)
	}
}

// Middleware runs outermost-first, and the error middleware sits innermost so
// it can render a problem the router raised.
func TestMiddlewareOrder(t *testing.T) {
	var order []string
	mark := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					order = append(order, name)
					next.ServeHTTP(w, r)
				})
		}
	}

	cfg := Resolve(
		WithMiddleware(mark("first"), mark("second")),
		WithErrorOptions(quiet()),
	)
	rec := run(t, Wrap(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "handler")
			apierr.Fail(r, apierr.NotFound("thing", 1))
		}), cfg))

	want := []string{"first", "second", "handler"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order = %v, want %v", order, want)
			break
		}
	}
	// The innermost error middleware still rendered the problem.
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestResolveAppliesInOrder(t *testing.T) {
	c := Resolve(
		WithErrorOptions(apierr.Options{TypeBaseURL: "https://a/"}),
		WithErrorOptions(apierr.Options{TypeBaseURL: "https://b/"}),
	)
	if c.Errors.TypeBaseURL != "https://b/" {
		t.Errorf("TypeBaseURL = %q, want the last option to win",
			c.Errors.TypeBaseURL)
	}
	if len(Resolve().Middleware) != 0 {
		t.Error("Resolve() with no options should leave Middleware empty")
	}
}

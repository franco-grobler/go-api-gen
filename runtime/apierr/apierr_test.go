package apierr

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// response is what a test observes: the middleware's output, already read.
type response struct {
	status int
	header http.Header
	body   []byte
}

// serve runs h behind the middleware and reads the whole response.
func serve(t *testing.T, opts Options, h http.HandlerFunc) response {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = quiet()
	}
	srv := Middleware(opts)(h)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/users", http.NoBody)
	srv.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return response{status: res.StatusCode, header: res.Header, body: body}
}

func decode(t *testing.T, res response) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(res.body, &got); err != nil {
		t.Fatalf("decoding response: %v\n%s", err, res.body)
	}
	return got
}

// mapOf and sliceOf assert with a message instead of panicking mid-chain.
func mapOf(t *testing.T, v any, path string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want a mapping", path, v)
	}
	return m
}

func sliceOf(t *testing.T, v any, path string) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is %T, want a sequence", path, v)
	}
	return s
}

// The whole point of the package: a schema failure and a handler-raised
// failure arrive in exactly the same shape.
func TestValidationProblemShape(t *testing.T) {
	v := validate.New(validate.Body)
	v.Field("name").Fail("string_too_short",
		"String should have at least 3 characters", "min_length", 3)
	v.Field("items").Index(0).Field("sku").Fail("missing", "Field required")

	res := serve(t, Options{
		TypeBaseURL: "https://errors.acme.io/",
		TraceID:     func(*http.Request) string { return "01J8X" },
	}, func(w http.ResponseWriter, r *http.Request) {
		Fail(r, FromValidator(v))
	})

	if res.status != ValidationStatus {
		t.Errorf("status = %d, want %d", res.status, ValidationStatus)
	}
	if ct := res.header.Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type = %q, want %q", ct, ContentType)
	}

	got := decode(t, res)
	checks := map[string]any{
		"type":     "https://errors.acme.io/validation_failed",
		"title":    "Request validation failed",
		"status":   float64(ValidationStatus),
		"detail":   "2 fields failed validation",
		"instance": "/v1/users",
		"trace_id": "01J8X",
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}

	errs := sliceOf(t, got["errors"], "errors")
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want two entries", got["errors"])
	}
	first := mapOf(t, errs[0], "errors[0]")
	if first["type"] != "string_too_short" {
		t.Errorf("errors[0].type = %v", first["type"])
	}
	loc := sliceOf(t, first["loc"], "errors[0].loc")
	if len(loc) != 2 || loc[0] != "body" || loc[1] != "name" {
		t.Errorf("errors[0].loc = %v, want [body name]", first["loc"])
	}
	ctx := mapOf(t, first["ctx"], "errors[0].ctx")
	if ctx["min_length"] != float64(3) {
		t.Errorf("errors[0].ctx = %v, want min_length 3", first["ctx"])
	}

	// A nested location keeps its index, which is what lets a client point at
	// the exact array element that failed.
	second := mapOf(t, errs[1], "errors[1]")
	nested := sliceOf(t, second["loc"], "errors[1].loc")
	if len(nested) != 4 || nested[0] != "body" || nested[1] != "items" ||
		nested[2] != float64(0) || nested[3] != "sku" {
		t.Errorf("errors[1].loc = %v, want [body items 0 sku]", second["loc"])
	}
}

func TestHandlerRaisedFieldErrorMatchesSchemaShape(t *testing.T) {
	res := serve(t, Options{}, func(w http.ResponseWriter, r *http.Request) {
		Fail(r, Validation().
			Field(validate.Body, "email").
			Fail("email_taken", "already registered"))
	})

	got := decode(t, res)
	if got["status"] != float64(ValidationStatus) {
		t.Errorf("status = %v", got["status"])
	}
	_ = res
	errs := sliceOf(t, got["errors"], "errors")
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one entry", got["errors"])
	}
	e := mapOf(t, errs[0], "errors[0]")
	if e["type"] != "email_taken" {
		t.Errorf("type = %v, want email_taken", e["type"])
	}
	loc := sliceOf(t, e["loc"], "errors[0].loc")
	if len(loc) != 2 || loc[1] != "email" {
		t.Errorf("loc = %v, want [body email]", e["loc"])
	}
	if got["detail"] != "1 field failed validation" {
		t.Errorf("detail = %v", got["detail"])
	}
}

// An unexpected error must not leak its message to the caller.
func TestInternalErrorIsWithheld(t *testing.T) {
	secret := "pq: password authentication failed for user \"admin\""

	res := serve(t, Options{}, func(w http.ResponseWriter, r *http.Request) {
		Fail(r, errors.New(secret))
	})
	if res.status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", res.status)
	}
	if strings.Contains(string(res.body), "password") {
		t.Errorf("internal error leaked into the response: %s", res.body)
	}
	if strings.Contains(string(res.body), "__cause") {
		t.Errorf("internal bookkeeping leaked into the response: %s", res.body)
	}

	// The operator can opt in.
	res = serve(t, Options{ExposeInternal: true},
		func(w http.ResponseWriter, r *http.Request) {
			Fail(r, errors.New(secret))
		})
	got := decode(t, res)
	if got["detail"] != secret {
		t.Errorf("detail = %v, want the cause when ExposeInternal is set",
			got["detail"])
	}
}

func TestPanicBecomesAProblem(t *testing.T) {
	res := serve(t, Options{}, func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	if res.status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.status)
	}
	got := decode(t, res)
	if got["title"] != "Internal server error" {
		t.Errorf("title = %v", got["title"])
	}
	if strings.Contains(string(res.body), "boom") {
		t.Errorf("panic value leaked into the response: %s", res.body)
	}
}

func TestBuilders(t *testing.T) {
	tests := []struct {
		name   string
		p      *Problem
		status int
		slug   string
	}{
		{"not found", NotFound("invoice", 7), 404, SlugNotFound},
		{"conflict", Conflict("already settled"), 409, SlugConflict},
		{"unauthorized", Unauthorized("no token"), 401, SlugUnauthorized},
		{"forbidden", Forbidden("not yours"), 403, SlugForbidden},
		{"bad request", BadRequest("unparseable"), 400, SlugBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.p.StatusCode() != tt.status {
				t.Errorf("status = %d, want %d", tt.p.StatusCode(), tt.status)
			}
			if tt.p.Type != tt.slug {
				t.Errorf("type = %q, want %q", tt.p.Type, tt.slug)
			}
			if tt.p.Error() == "" {
				t.Error("Error() is empty")
			}
		})
	}

	// NotFound names the resource as an extension member.
	got, err := json.Marshal(NotFound("invoice", 7))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"resource":"invoice"`) {
		t.Errorf("NotFound = %s, want a resource member", got)
	}
}

// errors.As must find a Problem so a caller can inspect the field errors.
func TestProblemIsAnError(t *testing.T) {
	var err error = Validation().
		Field(validate.Body, "email").
		Fail("email_taken", "already registered")

	var p *Problem
	if !errors.As(err, &p) {
		t.Fatal("errors.As did not find the Problem")
	}
	if len(p.Errors) != 1 || p.Errors[0].Type != "email_taken" {
		t.Errorf("Errors = %v", p.Errors)
	}
	if Convert(err) != p {
		t.Error("Convert should pass a Problem through unchanged")
	}
}

func TestExtraMembersCannotOverwriteStandardOnes(t *testing.T) {
	p := Validation().
		WithExtra("status", 200).
		WithExtra("hint", "check the email field")

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != float64(ValidationStatus) {
		t.Errorf("status = %v; an extension must not overwrite it",
			got["status"])
	}
	if got["hint"] != "check the email field" {
		t.Errorf("hint = %v, want the extension member", got["hint"])
	}
}

func TestResolveType(t *testing.T) {
	tests := []struct{ slug, base, want string }{
		{"validation_failed", "https://e.io/",
			"https://e.io/validation_failed"},
		{"validation_failed", "https://e.io", "https://e.io/validation_failed"},
		{"validation_failed", "", "validation_failed"},
		{"https://e.io/x", "https://other.io/", "https://e.io/x"},
		{"about:blank", "https://e.io/", "about:blank"},
		{"", "https://e.io/", ""},
	}
	for _, tt := range tests {
		if got := resolveType(tt.slug, tt.base); got != tt.want {
			t.Errorf("resolveType(%q, %q) = %q, want %q",
				tt.slug, tt.base, got, tt.want)
		}
	}
}

func TestFromValidatorReturnsNilWhenClean(t *testing.T) {
	if p := FromValidator(validate.New(validate.Body)); p != nil {
		t.Errorf("FromValidator = %v, want nil for a clean validator", p)
	}
	if p := FromValidator(nil); p != nil {
		t.Errorf("FromValidator(nil) = %v, want nil", p)
	}
}

// ValidationTitle is how a workspace renames validation failures without
// every call site passing a title. It applies wherever the problem came from.
func TestValidationTitleOverride(t *testing.T) {
	opts := Options{ValidationTitle: "Invalid request"}

	// Raised by the generated code, through the validator.
	v := validate.New()
	v.Field("email").Fail("missing", "Field required")
	res := serve(t, opts, func(w http.ResponseWriter, r *http.Request) {
		Fail(r, FromValidator(v))
	})
	if got := decode(t, res)["title"]; got != "Invalid request" {
		t.Errorf("title = %v, want the override", got)
	}

	// Raised by a handler, through the builder.
	res = serve(t, opts, func(w http.ResponseWriter, r *http.Request) {
		Fail(r, Validation().Field("body", "email").
			Fail("email_taken", "already registered"))
	})
	if got := decode(t, res)["title"]; got != "Invalid request" {
		t.Errorf("handler-raised title = %v, want the override", got)
	}
}

// A title the caller chose is theirs. The override replaces the default, not
// whatever happens to be there.
func TestValidationTitleLeavesADeliberateTitleAlone(t *testing.T) {
	res := serve(t, Options{ValidationTitle: "Invalid request"},
		func(w http.ResponseWriter, r *http.Request) {
			p := Validation()
			p.Title = "Payment details rejected"
			Fail(r, p.Field("body", "card").Fail("declined", "declined"))
		})
	if got := decode(t, res)["title"]; got != "Payment details rejected" {
		t.Errorf("title = %v, want the caller's own", got)
	}
}

// Only validation problems are renamed; a 404 keeps its own title.
func TestValidationTitleDoesNotTouchOtherProblems(t *testing.T) {
	res := serve(t, Options{ValidationTitle: "Invalid request"},
		func(w http.ResponseWriter, r *http.Request) {
			Fail(r, NotFound("user", 7))
		})
	if got := decode(t, res)["title"]; got != "Not found" {
		t.Errorf("title = %v, want the 404's own title", got)
	}
}

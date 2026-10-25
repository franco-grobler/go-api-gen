package petstore_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/petstore"
	"github.com/example/petstore/internal/api"
)

// serve returns the real store behind the generated server. Nothing here is a
// test double: what these tests check is what the generated layer does before
// Store is reached.
func serve() (*petstore.Service, http.Handler) {
	svc := petstore.New()
	return svc, petstore.NewServer(svc)
}

type result struct {
	status int
	body   []byte
}

func (r result) object(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, r.body)
	}
	return out
}

func (r result) array(t *testing.T) []any {
	t.Helper()
	var out []any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("response is not a JSON array: %v\n%s", err, r.body)
	}
	return out
}

func do(
	t *testing.T, h http.Handler, method, target, body string,
) result {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequestWithContext(t.Context(), method, target, reader)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	res := rec.Result()
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return result{status: res.StatusCode, body: raw}
}

func create(t *testing.T, h http.Handler, body string) result {
	t.Helper()
	return do(t, h, http.MethodPost, "/pets", body)
}

func TestCreateAndShow(t *testing.T) {
	_, srv := serve()

	res := create(t, srv, `{"name": "Fido", "tag": "dog"}`)
	if res.status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", res.status, res.body)
	}
	created := res.object(t)
	if created["id"] != float64(1) {
		t.Errorf("id = %v, want the store's assigned 1", created["id"])
	}
	if created["name"] != "Fido" || created["tag"] != "dog" {
		t.Errorf("created = %v, want Fido the dog", created)
	}

	res = do(t, srv, http.MethodGet, "/pets/1", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.status, res.body)
	}
	if got := res.object(t)["name"]; got != "Fido" {
		t.Errorf("name = %v, want Fido", got)
	}
}

// tag is optional, so an absent one is omitted rather than sent as null.
func TestOptionalTagIsOmitted(t *testing.T) {
	_, srv := serve()

	res := create(t, srv, `{"name": "Rex"}`)
	if res.status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", res.status, res.body)
	}
	if _, present := res.object(t)["tag"]; present {
		t.Errorf("body = %s, want tag omitted", res.body)
	}
}

// The request body is a NewPet, so an id a client tried to choose is not a
// field the generated code will decode into.
func TestClientCannotChooseTheID(t *testing.T) {
	_, srv := serve()

	res := create(t, srv, `{"id": 99, "name": "Fido"}`)
	if res.status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", res.status, res.body)
	}
	if got := res.object(t)["id"]; got != float64(1) {
		t.Errorf("id = %v, want the store's own 1", got)
	}
}

// Constraints in the spec are enforced before the handler runs, and reported
// in the RFC 9457 envelope with the configured type base URL.
//
// A field the client never sent is reported once, as missing. Its other
// constraints are not measured against the zero value, so a form binding to
// these errors shows one complaint per input.
func TestSchemaViolations(t *testing.T) {
	tests := []struct {
		name, body string
		want       []string
	}{{
		name: "missing required name",
		body: `{"tag": "dog"}`,
		want: []string{"missing at body/name"},
	}, {
		name: "name too short",
		body: `{"name": ""}`,
		want: []string{"string_too_short at body/name"},
	}, {
		name: "tag too long",
		body: `{"name": "Fido", "tag": "` + strings.Repeat("x", 65) + `"}`,
		want: []string{"string_too_long at body/tag"},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, srv := serve()
			res := create(t, srv, tt.body)
			if res.status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", res.status, res.body)
			}

			body := res.object(t)
			want := api.DefaultErrorOptions().TypeBaseURL + "validation_failed"
			if body["type"] != want {
				t.Errorf("type = %v, want %q", body["type"], want)
			}

			got := failures(t, body)
			if strings.Join(got, ", ") != strings.Join(tt.want, ", ") {
				t.Errorf("errors = %v, want %v", got, tt.want)
			}
			if n := store.Count(); n != 0 {
				t.Errorf("an invalid request reached the store: %d pets", n)
			}
		})
	}
}

// failures renders the errors array as "slug at a/b" strings, so a mismatch
// reports what actually came back rather than a length.
func failures(t *testing.T, body map[string]any) []string {
	t.Helper()
	entries, _ := body["errors"].([]any)
	out := make([]string, 0, len(entries))
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		loc, _ := e["loc"].([]any)
		parts := make([]string, len(loc))
		for i, seg := range loc {
			parts[i] = fmt.Sprint(seg)
		}
		out = append(out, fmt.Sprintf("%v at %s",
			e["type"], strings.Join(parts, "/")))
	}
	return out
}

// A limit outside the spec's range never reaches the handler, so the handler
// never has to defend against one.
func TestLimitIsRangeChecked(t *testing.T) {
	_, srv := serve()
	for _, target := range []string{"/pets?limit=0", "/pets?limit=101"} {
		res := do(t, srv, http.MethodGet, target, "")
		if res.status != http.StatusUnprocessableEntity {
			t.Errorf("GET %s = %d, want 422: %s",
				target, res.status, res.body)
		}
	}
}

func TestListHonoursLimit(t *testing.T) {
	_, srv := serve()
	for _, name := range []string{"Fido", "Rex", "Bella"} {
		if res := create(t, srv, `{"name": "`+name+`"}`); res.status !=
			http.StatusCreated {
			t.Fatalf("seeding %s failed with %d: %s",
				name, res.status, res.body)
		}
	}

	res := do(t, srv, http.MethodGet, "/pets?limit=2", "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.status, res.body)
	}
	if got := len(res.array(t)); got != 2 {
		t.Errorf("returned %d pets, want the 2 asked for", got)
	}

	res = do(t, srv, http.MethodGet, "/pets", "")
	if got := len(res.array(t)); got != 3 {
		t.Errorf("returned %d pets, want all 3", got)
	}
}

// A duplicate name is the store's own rule, raised with the same builder the
// generated validation uses, so it arrives in the same envelope.
func TestDuplicateNameIsAConflict(t *testing.T) {
	_, srv := serve()
	if res := create(t, srv, `{"name": "Fido"}`); res.status !=
		http.StatusCreated {
		t.Fatalf("seeding failed with %d: %s", res.status, res.body)
	}

	res := create(t, srv, `{"name": "Fido"}`)
	if res.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", res.status, res.body)
	}
	want := api.DefaultErrorOptions().TypeBaseURL + "conflict"
	if got := res.object(t)["type"]; got != want {
		t.Errorf("type = %v, want %q", got, want)
	}
}

func TestUnknownPetIsNotFound(t *testing.T) {
	_, srv := serve()
	res := do(t, srv, http.MethodGet, "/pets/7", "")
	if res.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", res.status, res.body)
	}
	body := res.object(t)
	if body["resource"] != "pet" {
		t.Errorf("resource = %v, want pet", body["resource"])
	}
}

// A path segment that is not a number is the generated layer's problem, not
// the handler's.
func TestMalformedIDIsRejectedBeforeTheHandler(t *testing.T) {
	_, srv := serve()
	res := do(t, srv, http.MethodGet, "/pets/not-a-number", "")
	if res.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", res.status, res.body)
	}
	errs, _ := res.object(t)["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", errs)
	}
	e, _ := errs[0].(map[string]any)
	loc, _ := e["loc"].([]any)
	if len(loc) != 2 || loc[0] != "path" || loc[1] != "petId" {
		t.Errorf("loc = %v, want [path petId]", loc)
	}
}

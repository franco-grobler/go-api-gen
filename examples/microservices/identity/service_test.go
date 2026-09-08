package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/microservices/identity/internal/api"
	"github.com/example/microservices/schema/apitypes"
)

const userID = "5b0a3f2e-0000-4000-8000-000000000001"

func ada() apitypes.User {
	return apitypes.User{
		ID:     userID,
		Email:  "ada@example.com",
		Status: apitypes.UserStatusActive,
	}
}

func get(t *testing.T, target string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	newServer(newService(ada())).ServeHTTP(rec, r)

	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return res.StatusCode, body
}

// What identity serves is the shared type, so what it writes decodes straight
// back into apitypes.User. This is the same round trip billing performs on the
// other side of the network, from its own module.
func TestGetUserServesTheSharedType(t *testing.T) {
	status, body := get(t, "/users/"+userID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}

	var got apitypes.User
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response does not decode as apitypes.User: %v\n%s",
			err, body)
	}
	want := ada()
	// Field by field: the struct carries an unexported missingFields slice,
	// which decoding populates and a literal does not, so it is not
	// comparable with ==.
	if got.ID != want.ID || got.Email != want.Email ||
		got.Status != want.Status {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}

	// An absent optional is omitted rather than sent as null.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["display_name"]; present {
		t.Errorf("body = %s, want display_name omitted", body)
	}
}

func TestUnknownUserIsNotFound(t *testing.T) {
	status, body := get(t, "/users/nobody")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}

	var problem map[string]any
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatal(err)
	}
	wantType := api.DefaultErrorOptions().TypeBaseURL + "not_found"
	if problem["type"] != wantType {
		t.Errorf("type = %v, want %q", problem["type"], wantType)
	}
}

// An empty path segment does not match the route, so the mux answers before
// any generated code runs.
func TestMissingIDIsNotRouted(t *testing.T) {
	status, _ := get(t, "/users/")
	if status != http.StatusNotFound {
		t.Errorf("GET /users/ = %d, want 404", status)
	}
}

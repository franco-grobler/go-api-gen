package identity_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/microservices/identity"
	"github.com/example/microservices/schema/apitypes"
)

const (
	userID = "5b0a3f2e-0000-4000-8000-000000000001"
	token  = "ada-token"
	other  = "7c1b4a3f-0000-4000-8000-000000000002"

	// A caller the authenticator knows and the store does not, which is the
	// only way to reach a 404: asking for somebody else is refused first, so
	// the store never says whether they exist.
	ghost      = "9d2c5b4e-0000-4000-8000-000000000003"
	ghostToken = "ghost-token"
)

func ada() apitypes.User {
	return apitypes.User{
		ID:     userID,
		Email:  "ada@example.com",
		Status: apitypes.UserStatusActive,
	}
}

// server is the real service behind the generated one, with an authenticator
// that knows one token. Nothing here is a double.
func server() http.Handler {
	tokens := identity.NewTokens(map[string]string{
		token:      userID,
		ghostToken: ghost,
	})
	return identity.NewServer(identity.New(ada()), tokens)
}

func get(t *testing.T, target string) (int, []byte) {
	t.Helper()
	return request(t, target, token)
}

// request sends target with the given bearer token, or none when it is empty.
func request(t *testing.T, target, bearer string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, target, http.NoBody)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	server().ServeHTTP(rec, r)

	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return res.StatusCode, body
}

// A request with no credential never reaches a handler, and comes back with
// the challenge RFC 9110 requires a 401 to carry.
func TestMissingTokenIsRefused(t *testing.T) {
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/users/"+userID, http.NoBody)
	rec := httptest.NewRecorder()
	server().ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", got)
	}
}

func TestUnknownTokenIsRefused(t *testing.T) {
	status, body := request(t, "/users/"+userID, "not-a-token")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", status, body)
	}
}

// Authentication says who the caller is; whether they may read this user is
// the handler's own rule, reached through the context the authenticator
// returned.
func TestCallerMayNotReadAnotherUser(t *testing.T) {
	status, body := request(t, "/users/"+other, token)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", status, body)
	}
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

// Authorisation runs before the lookup, so this asks for the caller's own
// record and finds the store has none.
func TestUnknownUserIsNotFound(t *testing.T) {
	status, body := request(t, "/users/"+ghost, ghostToken)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}

	var problem map[string]any
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatal(err)
	}
	wantType := identity.ErrorTypeBaseURL + "not_found"
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

func TestSeededStore(t *testing.T) {
	if n := identity.New(ada()).Count(); n != 1 {
		t.Errorf("Count() = %d, want 1", n)
	}
}

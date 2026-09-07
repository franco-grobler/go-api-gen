package identity_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/identity"
)

const userID = "22222222-2222-2222-2222-222222222222"

func serve() http.Handler {
	svc := identity.New(apitypes.User{
		ID:     uuid.MustParse(userID),
		Email:  "ada@acme.com",
		Status: apitypes.UserStatusActive,
	})
	return identity.NewServer(svc)
}

func get(t *testing.T, target string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	serve().ServeHTTP(rec, r)

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

func TestGetUser(t *testing.T) {
	status, body := get(t, "/users/"+userID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", status, body)
	}
	if body["email"] != "ada@acme.com" {
		t.Errorf("email = %v, want ada@acme.com", body["email"])
	}
	// An absent optional is omitted rather than sent as null, because
	// Optional[T] marshals with omitzero.
	if _, present := body["display_name"]; present {
		t.Errorf("display_name = %v, want it omitted", body["display_name"])
	}
}

// The handler never sees a malformed id: format: uuid is mapped onto
// uuid.UUID, so parsing is the generated layer's job and its failure is a 422
// rather than a panic inside GetUser.
func TestMalformedIDIsRejectedBeforeTheHandler(t *testing.T) {
	status, body := get(t, "/users/not-a-uuid")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %v", status, body)
	}
	errs, _ := body["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", body["errors"])
	}
	e, _ := errs[0].(map[string]any)
	loc, _ := e["loc"].([]any)
	if len(loc) != 2 || loc[0] != "path" || loc[1] != "userId" {
		t.Errorf("loc = %v, want [path userId]", e["loc"])
	}
}

// A well-formed id nobody has is the handler's own 404, raised with the same
// builder the generated code uses.
func TestUnknownUserIsNotFound(t *testing.T) {
	status, body := get(t, "/users/33333333-3333-3333-3333-333333333333")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %v", status, body)
	}
	want := identity.ErrorTypeBaseURL + "not_found"
	if body["type"] != want {
		t.Errorf("type = %v, want %q", body["type"], want)
	}
	if body["resource"] != "user" {
		t.Errorf("resource = %v, want user", body["resource"])
	}
}

package unions_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/unions"
)

// now is the clock the service reads, so a delay has an exact answer.
var now = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// send posts body to the generated server and returns the status and the
// decoded response.
func send(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	h := unions.NewServer(unions.New(func() time.Time { return now }))
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/notifications", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, rec.Body)
	}
	return rec.Code, out
}

// failures renders a problem's field errors as "type at loc".
func failures(problem map[string]any) []string {
	var out []string
	errs, _ := problem["errors"].([]any)
	for _, e := range errs {
		e, _ := e.(map[string]any)
		out = append(out, fmt.Sprintf("%v at %v", e["type"], e["loc"]))
	}
	return out
}

// The discriminator picks the channel, and an integer send_at is the delay
// member of SendAt.
func TestSMSAfterADelay(t *testing.T) {
	code, got := send(t, `{
		"channel": {"type": "sms", "number": "+447700900123"},
		"message": "Your table is ready", "send_at": 300}`)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d: %v", code, got)
	}
	if got["send_at"] != "2026-10-26T09:05:00Z" {
		t.Errorf("send_at = %v, want five minutes from now", got["send_at"])
	}
	// The union encodes as the member that was set, nothing more.
	channel, _ := json.Marshal(got["channel"])
	if string(channel) != `{"number":"+447700900123","type":"sms"}` {
		t.Errorf("channel = %s", channel)
	}
}

// A string send_at is the moment member, decoded as a time.Time.
func TestEmailAtAMoment(t *testing.T) {
	code, got := send(t, `{
		"channel": {"type": "email", "address": "ada@example.com"},
		"message": "Invoice", "send_at": "2026-12-01T08:00:00Z"}`)
	if code != http.StatusAccepted || got["send_at"] != "2026-12-01T08:00:00Z" {
		t.Errorf("status = %d, send_at = %v", code, got["send_at"])
	}
}

// What the generated code rejects before the handler runs, each at the
// location of the union or of the member's own field.
func TestRejected(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"unknown channel": {
			`{"channel": {"type": "pigeon"}, "message": "hi"}`,
			"[union_tag_invalid at [body channel]]",
		},
		"no channel type": {
			`{"channel": {"number": "+447700900123"}, "message": "hi"}`,
			"[union_tag_not_found at [body channel]]",
		},
		"the chosen member's own rules": {
			`{"channel": {"type": "sms", "number": "07700"}, "message": "hi"}`,
			"[string_pattern_mismatch at [body channel number]]",
		},
		"a delay out of range": {
			`{"channel": {"type": "sms", "number": "+447700900123"},
			  "message": "hi", "send_at": 0}`,
			"[greater_than_equal at [body send_at]]",
		},
		"neither a moment nor a delay": {
			`{"channel": {"type": "sms", "number": "+447700900123"},
			  "message": "hi", "send_at": true}`,
			"[union_no_match at [body send_at]]",
		},
		// The handler's own rule, reported the same way.
		"a plain-http webhook": {
			`{"channel": {"type": "webhook", "url": "http://example.com/hook"},
			  "message": "hi"}`,
			"[url_scheme at [body channel url]]",
		},
	} {
		t.Run(name, func(t *testing.T) {
			code, got := send(t, tc.body)
			if code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", code)
			}
			if f := fmt.Sprint(failures(got)); f != tc.want {
				t.Errorf("errors = %s, want %s", f, tc.want)
			}
		})
	}
}

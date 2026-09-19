// Package apierr renders API errors as RFC 9457 problem documents.
//
// Every error leaves the process in one shape, whether it came from a schema
// constraint, a custom x-validate rule, or a handler that looked something up
// in a database and did not like the answer. The envelope describes the
// request-level failure; the errors array describes the individual fields,
// following the shape FastAPI produces so that a frontend can bind failures
// back to form inputs.
package apierr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

// ContentType is the media type of a problem document.
const ContentType = "application/problem+json"

// FieldError is one field-level failure. It is the same shape the validate
// package accumulates.
type FieldError = validate.FieldError

// Problem is an RFC 9457 problem document.
//
// XxxError convention would make it less recognisable, not more.
//
// Errors is an extension member: RFC 9457 describes the request-level failure
// but says nothing about per-field detail, and a validation response is far
// more useful with it.
//
//nolint:errname // "Problem" is the name RFC 9457 gives this document; the
type Problem struct {
	Type     string       `json:"type,omitempty"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	TraceID  string       `json:"trace_id,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`

	// Extra members are flattened into the top-level object on marshal, which
	// is how RFC 9457 extends a problem document.
	Extra map[string]any `json:"-"`

	// headers are response headers the problem is sent with, set through
	// WithHeader. They are not members of the document.
	headers map[string]string
}

// Error implements error.
func (p *Problem) Error() string {
	if p.Detail != "" {
		return fmt.Sprintf("%s: %s", p.Title, p.Detail)
	}
	if n := len(p.Errors); n > 0 {
		return fmt.Sprintf("%s (%d field error(s))", p.Title, n)
	}
	return p.Title
}

// StatusCode returns the HTTP status, defaulting to 500 when unset.
func (p *Problem) StatusCode() int {
	if p.Status == 0 {
		return http.StatusInternalServerError
	}
	return p.Status
}

// MarshalJSON flattens Extra into the object alongside the standard members.
//
// when it is not addressable; the copy is not on any hot path.
//
//nolint:gocritic // A value receiver so a Problem marshals correctly even
func (p Problem) MarshalJSON() ([]byte, error) {
	type alias Problem // avoid recursing into this method
	base, err := json.Marshal(alias(p))
	if err != nil {
		return nil, err
	}
	if len(p.Extra) == 0 {
		return base, nil
	}

	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	// Sorted so the output is stable, and standard members win: an extension
	// must not be able to overwrite the status a handler chose.
	keys := make([]string, 0, len(p.Extra))
	for k := range p.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, taken := merged[k]; taken {
			continue
		}
		raw, err := json.Marshal(p.Extra[k])
		if err != nil {
			return nil, fmt.Errorf("extra member %q: %w", k, err)
		}
		merged[k] = raw
	}
	return json.Marshal(merged)
}

// Write sends the problem document as the response.
func (p *Problem) Write(w http.ResponseWriter) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	for key, value := range p.headers {
		w.Header().Set(key, value)
	}
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(p.StatusCode())
	_, err = w.Write(body)
	return err
}

// WithHeader attaches a response header the problem must be sent with.
//
// A 401 is the reason this exists: RFC 9110 requires it to carry a
// WWW-Authenticate challenge, and a client given no challenge cannot know what
// to send instead. The headers are not part of the JSON body.
func (p *Problem) WithHeader(key, value string) *Problem {
	if p.headers == nil {
		p.headers = map[string]string{}
	}
	p.headers[key] = value
	return p
}

// WithExtra attaches an extension member.
func (p *Problem) WithExtra(key string, value any) *Problem {
	if p.Extra == nil {
		p.Extra = map[string]any{}
	}
	p.Extra[key] = value
	return p
}

// WithDetail sets the human-readable explanation.
func (p *Problem) WithDetail(format string, args ...any) *Problem {
	if len(args) == 0 {
		p.Detail = format
	} else {
		p.Detail = fmt.Sprintf(format, args...)
	}
	return p
}

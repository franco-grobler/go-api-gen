package apierr

import (
	"fmt"
	"net/http"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

// Slugs are the machine-readable type identifiers for the built-in problems.
const (
	SlugValidation   = "validation_failed"
	SlugNotFound     = "not_found"
	SlugConflict     = "conflict"
	SlugUnauthorized = "unauthorized"
	SlugForbidden    = "forbidden"
	SlugBadRequest   = "bad_request"
	SlugInternal     = "internal_error"
)

// ValidationStatus is the status used for a request that parsed but failed
// validation. 422 distinguishes it from a request that was not understood at
// all, which is what makes a client able to act on the errors array.
const ValidationStatus = http.StatusUnprocessableEntity

// New returns a problem with the given status and slug.
func New(status int, slug, title string) *Problem {
	return &Problem{Type: slug, Title: title, Status: status}
}

// Validation returns an empty validation problem, ready for Field and Fail.
func Validation() *Problem {
	return New(ValidationStatus, SlugValidation, "Request validation failed")
}

// FromValidator returns a validation problem carrying the accumulated
// failures, or nil when nothing failed.
func FromValidator(v *validate.V) *Problem {
	if v == nil || !v.Failed() {
		return nil
	}
	p := Validation()
	p.Errors = v.Errors()
	p.Detail = describe(len(p.Errors))
	return p
}

func describe(n int) string {
	if n == 1 {
		return "1 field failed validation"
	}
	return fmt.Sprintf("%d fields failed validation", n)
}

// NotFound returns a 404 for a named resource.
func NotFound(resource string, id any) *Problem {
	p := New(http.StatusNotFound, SlugNotFound, "Not found")
	if resource != "" {
		p.Detail = fmt.Sprintf("no %s with id %v", resource, id)
		return p.WithExtra("resource", resource)
	}
	return p
}

// Conflict returns a 409.
func Conflict(format string, args ...any) *Problem {
	return New(http.StatusConflict, SlugConflict, "Conflict").
		WithDetail(format, args...)
}

// Unauthorized returns a 401.
func Unauthorized(format string, args ...any) *Problem {
	return New(http.StatusUnauthorized, SlugUnauthorized, "Unauthorized").
		WithDetail(format, args...)
}

// Forbidden returns a 403.
func Forbidden(format string, args ...any) *Problem {
	return New(http.StatusForbidden, SlugForbidden, "Forbidden").
		WithDetail(format, args...)
}

// BadRequest returns a 400, for a request that could not be understood at all.
// A request that parsed but failed a rule should use Validation instead.
func BadRequest(format string, args ...any) *Problem {
	return New(http.StatusBadRequest, SlugBadRequest, "Bad request").
		WithDetail(format, args...)
}

// Internal returns a 500. The detail is deliberately not taken from the
// underlying error: see Middleware.
func Internal() *Problem {
	return New(http.StatusInternalServerError, SlugInternal,
		"Internal server error")
}

// Field starts a field-level failure at the given location path, which begins
// with a request part such as "body".
//
//	apierr.Validation().
//	    Field("body", "email").
//	    Fail("email_taken", "already registered")
func (p *Problem) Field(loc ...any) *FieldBuilder {
	return &FieldBuilder{problem: p, loc: loc}
}

// FieldBuilder attaches one failure to a problem.
type FieldBuilder struct {
	problem *Problem
	loc     []any
}

// Index appends an array index to the location.
func (b *FieldBuilder) Index(i int) *FieldBuilder {
	b.loc = append(b.loc, i)
	return b
}

// Key appends a member name to the location.
func (b *FieldBuilder) Key(name string) *FieldBuilder {
	b.loc = append(b.loc, name)
	return b
}

// Fail records the failure and returns the problem, so a handler can return it
// directly.
func (b *FieldBuilder) Fail(slug, msg string, ctx ...any) *Problem {
	e := FieldError{Type: slug, Loc: b.loc, Msg: msg}
	if len(ctx) >= 2 {
		e.Ctx = make(map[string]any, len(ctx)/2)
		for i := 0; i+1 < len(ctx); i += 2 {
			key, ok := ctx[i].(string)
			if !ok {
				continue
			}
			e.Ctx[key] = ctx[i+1]
		}
	}
	b.problem.Errors = append(b.problem.Errors, e)
	b.problem.Detail = describe(len(b.problem.Errors))
	return b.problem
}

package apierr

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
)

// Options configures the middleware and the response writer it installs.
type Options struct {
	// TypeBaseURL is prefixed to a slug to form the RFC 9457 type URI. With it
	// unset, the slug is emitted bare, which is still a valid type value.
	TypeBaseURL string

	// ValidationTitle replaces the title of a validation failure, whether the
	// generated code raised it or a handler did. With it unset,
	// DefaultValidationTitle stands.
	ValidationTitle string

	// TraceID extracts a correlation id from the request. When it returns "",
	// no trace_id member is emitted.
	TraceID func(*http.Request) string

	// Logger records 5xx problems and recovered panics. Defaults to
	// slog.Default().
	Logger *slog.Logger

	// ExposeInternal includes the underlying error's message in a 500's
	// detail. Off by default: an internal error message is for the operator,
	// not the caller.
	ExposeInternal bool
}

// contextKey is unexported so no other package can collide with it.
type contextKey struct{ name string }

var problemKey = &contextKey{"apierr.problem"}

// Middleware renders any problem raised while handling a request.
//
// It recovers panics, stamps instance and trace_id, and resolves the type URI.
// A handler reports a failure by calling Fail with the request, which is what
// the generated server does; nothing here inspects the response body.
func Middleware(opts Options) func(http.Handler) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			slot := &slot{}
			r = r.WithContext(context.WithValue(r.Context(), problemKey, slot))

			defer func() {
				if rec := recover(); rec != nil {
					opts.Logger.ErrorContext(r.Context(),
						"panic handling request",
						"method", r.Method,
						"path", r.URL.Path,
						"panic", rec,
						"stack", string(debug.Stack()))
					write(w, r, Internal(), &opts)
				}
			}()

			next.ServeHTTP(w, r)

			if p := slot.problem; p != nil && !slot.written {
				slot.written = true
				write(w, r, p, &opts)
			}
		}
		return http.HandlerFunc(fn)
	}
}

// slot carries the problem a handler raised back out to the middleware.
type slot struct {
	problem *Problem
	written bool
}

// Fail records an error against the request so the middleware renders it.
//
// Any error converts: a *Problem is used as-is, and anything else becomes a
// 500 whose detail is withheld unless Options.ExposeInternal is set.
func Fail(r *http.Request, err error) {
	if err == nil {
		return
	}
	s, ok := r.Context().Value(problemKey).(*slot)
	if !ok {
		return
	}
	s.problem = Convert(err)
}

// Convert maps an error onto a Problem.
func Convert(err error) *Problem {
	if err == nil {
		return nil
	}
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	// The message is attached but only surfaced when explicitly allowed, so a
	// database error cannot leak through a 500 by accident.
	return Internal().WithExtra("__cause", err.Error())
}

// Raised reports the problem recorded for a request, if any.
func Raised(r *http.Request) *Problem {
	s, ok := r.Context().Value(problemKey).(*slot)
	if !ok {
		return nil
	}
	return s.problem
}

// write finalises and sends a problem.
func write(w http.ResponseWriter, r *http.Request, p *Problem, opts *Options) {
	if p.Instance == "" {
		p.Instance = r.URL.Path
	}
	if p.TraceID == "" && opts.TraceID != nil {
		p.TraceID = opts.TraceID(r)
	}
	// Before the slug becomes a URI, while it is still comparable to
	// SlugValidation. A title the caller chose deliberately is not replaced.
	if opts.ValidationTitle != "" && p.Type == SlugValidation &&
		p.Title == DefaultValidationTitle {
		p.Title = opts.ValidationTitle
	}
	p.Type = resolveType(p.Type, opts.TypeBaseURL)

	cause, hasCause := p.Extra["__cause"].(string)
	delete(p.Extra, "__cause")
	if len(p.Extra) == 0 {
		p.Extra = nil
	}

	if p.StatusCode() >= http.StatusInternalServerError {
		if hasCause && cause != "" {
			opts.Logger.ErrorContext(r.Context(), "request failed",
				"method", r.Method, "path", r.URL.Path, "error", cause)
			if opts.ExposeInternal {
				p.Detail = cause
			}
		}
	}

	if err := p.Write(w); err != nil {
		opts.Logger.ErrorContext(r.Context(), "writing problem response failed",
			"error", err)
	}
}

// resolveType turns a slug into a type URI. A value that is already a URI, or
// the RFC's "about:blank", is left alone.
func resolveType(slug, base string) string {
	switch {
	case slug == "":
		return ""
	case base == "":
		return slug
	case strings.Contains(slug, ":"):
		return slug
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + slug
}

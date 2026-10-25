// Package httpx holds the parts of a generated server that do not vary.
//
// Only the routing table and the handler interface differ between services;
// option plumbing, middleware ordering, and response writing are identical
// everywhere. Generating a copy of them into each service would be the same
// duplication this tool exists to remove from schemas.
package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/franco-grobler/go-api-gen/runtime/apierr"
)

// Config is the resolved configuration of a generated server.
type Config struct {
	// Middleware wraps every route, outermost first.
	Middleware []func(http.Handler) http.Handler
	// Errors configures how problems are rendered.
	Errors apierr.Options
}

// Option configures a generated server. Each generated package aliases this
// as its own ServerOption so a caller needs only that package.
type Option func(*Config)

// WithMiddleware wraps every route, outermost first.
func WithMiddleware(mw ...func(http.Handler) http.Handler) Option {
	return func(c *Config) {
		c.Middleware = append(c.Middleware, mw...)
	}
}

// WithErrorOptions configures how problems are rendered.
func WithErrorOptions(opts apierr.Options) Option {
	return func(c *Config) { c.Errors = opts }
}

// Resolve applies options in order.
func Resolve(opts ...Option) Config {
	var c Config
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// Wrap layers the middleware around a router.
//
// The error middleware goes innermost, so a problem raised while decoding a
// request is rendered before any caller-supplied middleware sees the response.
//
// The config is taken by value: Wrap runs once, when a server is built, so a
// pointer would save nothing and would let a caller mutate the configuration
// out from under a running server.
//
//nolint:gocritic // hugeParam: taking Config by value is deliberate, above.
func Wrap(router http.Handler, c Config) http.Handler {
	handler := apierr.Middleware(c.Errors)(router)
	for i := len(c.Middleware) - 1; i >= 0; i-- {
		handler = c.Middleware[i](handler)
	}
	return handler
}

// WriteJSON sends a successful JSON response.
//
// The body is marshalled before the status is written, so a marshalling
// failure can still be reported as a problem instead of tearing a half-written
// response.
func WriteJSON(
	w http.ResponseWriter, r *http.Request, status int, body any,
) {
	encoded, err := json.Marshal(body)
	if err != nil {
		apierr.Fail(r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so a write failure means the client
	// went away. There is nobody left to report it to.
	_, _ = w.Write(encoded) //nolint:errcheck // see above
}

// WriteStatus sends a successful response with no body.
func WriteStatus(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}

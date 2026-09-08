package petstore

import (
	"log/slog"
	"net/http"
	"time"
)

// RequestLogger logs one line per request, and is the shape every
// api.WithMiddleware value takes: an http.Handler wrapper, applied outermost
// first, wrapped around the generated routing.
//
// It sits outside the error middleware, so by the time it records a status the
// problem document has already been written.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			logger.InfoContext(r.Context(), "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start))
		})
	}
}

// statusRecorder remembers the status so the log line can report it. Nothing
// in net/http exposes it after the fact.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

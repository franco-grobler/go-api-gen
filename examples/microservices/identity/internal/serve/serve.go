// Package serve turns a handler into a process: a listener with its timeouts,
// a shutdown that waits for in-flight requests, and one line of log each.
//
// ../../../monorepo keeps one copy of this for both its services, because they
// are one module. Here they are not, so each carries its own. That is the
// trade separate deployables make, and it is worth seeing next to the thing
// they do still share: the contract, in the schema module.
package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Grace is how long an in-flight request has to finish once a termination
// signal arrives.
const Grace = 10 * time.Second

// Run serves h on addr until the process is asked to stop, then drains. It
// returns nil on a clean shutdown.
func Run(name, addr string, h http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: h,

		// ReadHeaderTimeout is the one that stops a client holding a
		// connection open by sending headers one byte at a time.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listening := make(chan error, 1)
	go func() {
		logger.Info("listening", "service", name, "addr", addr)
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		listening <- err
	}()

	select {
	case err := <-listening:
		// The listener gave up on its own: a port already in use, usually.
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down", "service", name, "grace", Grace)
	shutdown, cancel := context.WithTimeout(context.Background(), Grace)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	return <-listening
}

// Logger returns the logger this service writes.
func Logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, nil))
}

// EnvOr reads an environment variable, falling back when it is unset.
func EnvOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// RequestLogger logs one line per request. It runs outside the error
// middleware, so by the time it records a status the problem document has
// already been written.
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

// statusRecorder remembers the status so the log line can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

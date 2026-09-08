// Command petstored serves the pet store over HTTP.
//
// It is a complete, runnable program: flags, a logger, the generated server
// behind one middleware, and a shutdown that waits for in-flight requests.
// The database is a map, so state lasts as long as the process.
//
//	go run ./cmd/petstored -addr :8080
//	curl -s localhost:8080/pets
//	curl -s -X POST localhost:8080/pets \
//	  -H 'content-type: application/json' -d '{"name":"Fido","tag":"dog"}'
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/petstore"
)

// shutdownGrace is how long an in-flight request has to finish once a
// termination signal arrives.
const shutdownGrace = 10 * time.Second

func main() {
	addr := flag.String("addr", envOr("PETSTORE_ADDR", ":8080"),
		"address to listen on")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(*addr, logger); err != nil {
		logger.Error("petstored failed", "error", err)
		os.Exit(1)
	}
}

func run(addr string, logger *slog.Logger) error {
	svc := petstore.New()
	srv := &http.Server{
		Addr: addr,
		Handler: petstore.NewServer(svc,
			petstore.WithMiddleware(petstore.RequestLogger(logger))),

		// A public listener needs every one of these. ReadHeaderTimeout on its
		// own is what stops a client from holding a connection open by sending
		// headers one byte at a time.
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
		logger.Info("listening", "addr", addr)
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

	logger.Info("shutting down", "grace", shutdownGrace)
	shutdown, cancel := context.WithTimeout(
		context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	return <-listening
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

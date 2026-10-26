// Command notifyd schedules notifications over HTTP.
//
// Nothing is sent: the service records what it would send, for as long as
// the process lives.
//
//	go run ./cmd/notifyd
//	curl -s -X POST localhost:8080/notifications \
//	  -H 'content-type: application/json' \
//	  -d '{"channel": {"type": "sms", "number": "+447700900123"},
//	       "message": "Your table is ready", "send_at": 300}'
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

	"github.com/example/unions"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(*addr, logger); err != nil {
		logger.Error("notifyd failed", "error", err)
		os.Exit(1)
	}
}

func run(addr string, logger *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           unions.NewServer(unions.New(time.Now)),
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
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(
		context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

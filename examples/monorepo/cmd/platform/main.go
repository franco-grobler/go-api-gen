// Command platform runs both services, each on its own listener.
//
// A deployment would build one binary per service. They are together here so
// the example has a single place showing the wiring, and so that CI compiles
// it: neither service package exports anything a main cannot reach, because
// internal/api is internal to the service that owns it.
package main

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/services/billing"
	"github.com/acme/monorepo/services/identity"
)

func main() {
	// One apitypes.User value, handed to both services. It is the same Go
	// type in each, so there is no conversion here to write.
	ada := apitypes.User{
		ID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Email:  "ada@acme.com",
		Status: apitypes.UserStatusActive,
	}

	go serve(":8081", identity.NewServer(identity.New(ada)))
	serve(":8080", billing.NewServer(billing.New(ada)))
}

func serve(addr string, handler http.Handler) {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("%s: %v", addr, err)
	}
}

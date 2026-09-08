// Command billing serves the billing service.
//
// One binary per service, from one module: the two mains here differ only in
// which service they construct, because internal/platform holds everything
// else. The database is a map, so state lasts as long as the process.
//
//	go run ./cmd/billing -addr :8080
//	curl -s localhost:8080/invoices
package main

import (
	"os"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/internal/platform"
	"github.com/acme/monorepo/services/billing"
)

func main() {
	addr := platform.EnvOr("BILLING_ADDR", ":8080")
	logger := platform.Logger()

	// Seeded rather than fetched: billing does not call identity in this
	// example, it is handed the same apitypes.User value identity would serve.
	svc := billing.New(seedPayer())

	err := platform.Run("billing", addr,
		billing.NewServer(svc,
			billing.WithMiddleware(platform.RequestLogger(logger))),
		logger)
	if err != nil {
		logger.Error("billing failed", "error", err)
		os.Exit(1)
	}
}

func seedPayer() apitypes.User {
	return apitypes.User{
		ID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Email:  "ada@acme.com",
		Status: apitypes.UserStatusActive,
	}
}

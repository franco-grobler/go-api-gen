// Command identity serves the identity service.
//
// The database is a map, so state lasts as long as the process.
//
//	go run ./cmd/identity -addr :8081
//	curl -s localhost:8081/users/22222222-2222-2222-2222-222222222222
package main

import (
	"os"

	"github.com/google/uuid"

	"github.com/acme/monorepo/gen/apitypes"
	"github.com/acme/monorepo/internal/platform"
	"github.com/acme/monorepo/services/identity"
)

func main() {
	addr := platform.EnvOr("IDENTITY_ADDR", ":8081")
	logger := platform.Logger()

	svc := identity.New(apitypes.User{
		ID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Email:  "ada@acme.com",
		Status: apitypes.UserStatusActive,
	})

	err := platform.Run("identity", addr,
		identity.NewServer(svc,
			identity.WithMiddleware(platform.RequestLogger(logger))),
		logger)
	if err != nil {
		logger.Error("identity failed", "error", err)
		os.Exit(1)
	}
}

// Command identityd serves the identity service over HTTP.
//
// The database is a map, so state lasts as long as the process.
//
//	go run ./cmd/identityd
//	curl -s localhost:8081/users/5b0a3f2e-0000-4000-8000-000000000001
package main

import (
	"os"

	"github.com/example/microservices/identity"
	"github.com/example/microservices/identity/internal/serve"
	"github.com/example/microservices/schema/apitypes"
)

func main() {
	addr := serve.EnvOr("IDENTITY_ADDR", ":8081")
	logger := serve.Logger()

	svc := identity.New(apitypes.User{
		ID:     "5b0a3f2e-0000-4000-8000-000000000001",
		Email:  "ada@example.com",
		Status: apitypes.UserStatusActive,
	})

	err := serve.Run("identity", addr,
		identity.NewServer(svc,
			identity.WithMiddleware(serve.RequestLogger(logger))),
		logger)
	if err != nil {
		logger.Error("identityd failed", "error", err)
		os.Exit(1)
	}
}

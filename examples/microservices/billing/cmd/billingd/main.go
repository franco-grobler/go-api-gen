// Command billingd serves the billing service over HTTP.
//
// The database is a map, so state lasts as long as the process. Run identityd
// alongside it to fetch a payer to invoice:
//
//	go run ./cmd/billingd
//	curl -s localhost:8081/users/5b0a3f2e-0000-4000-8000-000000000001 \
//	  | jq '{payer: ., total: {amount_minor: 1500, currency: "GBP"}}' \
//	  | curl -s -X POST localhost:8080/invoices \
//	      -H 'content-type: application/json' -d @-
package main

import (
	"os"

	"github.com/example/microservices/billing"
	"github.com/example/microservices/billing/internal/serve"
)

func main() {
	addr := serve.EnvOr("BILLING_ADDR", ":8080")
	logger := serve.Logger()

	svc := billing.New()

	err := serve.Run("billing", addr,
		billing.NewServer(svc,
			billing.WithMiddleware(serve.RequestLogger(logger))),
		logger)
	if err != nil {
		logger.Error("billingd failed", "error", err)
		os.Exit(1)
	}
}

package main

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/example/microservices/schema/apitypes"
)

const addr = ":8081"

func main() {
	svc := newService(apitypes.User{
		ID:     "5b0a3f2e-0000-4000-8000-000000000001",
		Email:  "ada@example.com",
		Status: apitypes.UserStatusActive,
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           newServer(svc),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("identity listening on %s", addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

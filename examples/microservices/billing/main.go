package main

import (
	"errors"
	"log"
	"net/http"
	"time"
)

const addr = ":8080"

func main() {
	srv := &http.Server{
		Addr:              addr,
		Handler:           newServer(newService()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("billing listening on %s", addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

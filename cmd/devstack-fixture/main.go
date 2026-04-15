package main

import (
	"flag"
	"log"
	"net/http"

	"llama_shim/internal/devstackfixture"
)

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	flag.Parse()

	server := &http.Server{
		Addr:    *addr,
		Handler: devstackfixture.NewHandler(),
	}

	log.Printf("devstack fixture listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("devstack fixture stopped: %v", err)
	}
}

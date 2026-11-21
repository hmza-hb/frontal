package main

import (
	"log"
	"net/http"
	"os"

	"frontal/services/api/internal/health"
)

func main() {
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}
	if err := http.ListenAndServe(address, health.NewMux()); err != nil {
		log.Fatal(err)
	}
}

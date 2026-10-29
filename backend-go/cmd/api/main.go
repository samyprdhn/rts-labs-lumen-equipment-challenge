package main

import (
	"log"
	"net/http"
	"time"

	"lumen/equipment-api/internal/config"
	"lumen/equipment-api/internal/db"
	"lumen/equipment-api/internal/handlers"
	"lumen/equipment-api/internal/server"
)

func main() {
	config.LoadEnv()
	db.Open()

	handlers.InitFleetCare(
		config.Get("MAINTENANCE_API_URL", "http://localhost:4010"),
		config.Get("FLEETCARE_API_KEY", "fc_test_key"),
		3*time.Second,  // per-call timeout, bounded well under the mock's measured worst-case hang
		5*time.Minute,  // cache TTL - maintenance windows are planned days ahead
		15*time.Minute, // staleAfter = TTL x 3; past this, unrefreshed data reads as unknown, never ok
	)

	port := config.Get("PORT", "8080")
	log.Printf("Lumen equipment API (Go) listening on http://localhost:%s", port)
	// A bare http.ListenAndServe has no read/write/idle timeouts at all - a
	// slow or stalled client can hold a goroutine open indefinitely
	// (docs/reviews/2026-10-28-part2-reservations.md). Part 1 only served
	// GET requests, so this went unnoticed; Part 2 adds the first endpoint
	// that reads a client body, where it becomes worth fixing. The body
	// size cap itself is enforced per-request in server.Wrap, not here.
	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           server.New(),
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(httpServer.ListenAndServe())
}

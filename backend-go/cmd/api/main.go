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
	log.Fatal(http.ListenAndServe(":"+port, server.New()))
}

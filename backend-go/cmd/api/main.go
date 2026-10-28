package main

import (
	"log"
	"net/http"

	"lumen/equipment-api/internal/config"
	"lumen/equipment-api/internal/db"
	"lumen/equipment-api/internal/server"
)

func main() {
	config.LoadEnv()
	db.Open()

	port := config.Get("PORT", "8080")
	log.Printf("Lumen equipment API (Go) listening on http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, server.New()))
}

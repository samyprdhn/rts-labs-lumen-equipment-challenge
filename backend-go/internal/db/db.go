package db

import (
	"database/sql"
	"log"
	"path/filepath"

	"lumen/equipment-api/internal/config"
)

var DB *sql.DB

func Open() {
	path := filepath.Join(config.RepoRoot(), config.Get("DB_PATH", "db/lumen.db"))
	var err error
	DB, err = sql.Open(driverName, path)
	if err != nil {
		log.Fatal(err)
	}
}

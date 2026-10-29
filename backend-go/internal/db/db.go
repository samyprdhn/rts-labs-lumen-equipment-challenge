package db

import (
	"database/sql"
	"log"
	"path/filepath"

	"lumen/equipment-api/internal/config"
)

var DB *sql.DB

func Open() {
	dbPath := config.Get("DB_PATH", "db/lumen.db")
	path := dbPath
	if !filepath.IsAbs(dbPath) {
		// An absolute DB_PATH (e.g. a disposable test database, or a
		// mounted volume path in some deployments) is used as-is;
		// filepath.Join would otherwise prefix it with RepoRoot and
		// silently produce a wrong, usually nonexistent, path.
		path = filepath.Join(config.RepoRoot(), dbPath)
	}
	// Both pragmas are passed in the DSN, not run once via DB.Exec, because
	// DB is a connection pool: an Exec after Open only touches whichever one
	// connection services that call, leaving every other pooled connection
	// without it. journal_mode happens to be file-persisted so an Exec would
	// have worked for it by accident, but busy_timeout is a per-connection
	// runtime setting - without this, most requests would silently run with
	// no busy_timeout at all. modernc.org/sqlite runs a "_pragma=NAME(VALUE)"
	// DSN parameter as its own PRAGMA on every new connection as it's opened.
	//
	// WAL mode: a reservation write (Part 2) holds SQLite's single write
	// lock for the duration of its transaction; without WAL, readers
	// (availability checks, Part 1) would block on that writer too.
	// Verified empirically in docs/decisions/0002-reservation-concurrency-control.md
	// (a read completed in ~46ms while a writer held a 300ms lock, under WAL).
	//
	// busy_timeout bounds how long a writer blocks on SQLite's single write
	// lock before giving up with SQLITE_BUSY (ADR 0002): ordinary contention
	// between a couple of foremen a few hundred ms apart resolves silently;
	// pathological contention still bounds out to a catchable error instead
	// of hanging. 3s matches the FleetCare client's own timeout bound from
	// Part 1.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)"
	var err error
	DB, err = sql.Open(driverName, dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := DB.Ping(); err != nil {
		log.Fatal("db: failed to open/configure database: ", err)
	}
}

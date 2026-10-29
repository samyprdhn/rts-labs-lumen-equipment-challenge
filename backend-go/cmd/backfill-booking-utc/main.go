// backfill-booking-utc is a one-time migration for Part 2
// (docs/decisions/0002-reservation-concurrency-control.md): it populates
// the new, additive start_at_utc/end_at_utc columns on existing bookings
// rows, reusing Part 1's already-proven multi-format date parser
// (internal/availability.ParseBookingTime) rather than reimplementing
// parsing logic.
//
// Idempotent: only processes rows where start_at_utc IS NULL, so running
// it twice is safe. Never deletes, rewrites, or reorders existing rows or
// columns - only fills in the two new ones.
//
// Usage: REPO_ROOT=.. go run ./cmd/backfill-booking-utc [-dry-run]
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"lumen/equipment-api/internal/availability"
	"lumen/equipment-api/internal/config"
	"lumen/equipment-api/internal/db"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report what would change without writing")
	flag.Parse()

	config.LoadEnv()
	db.Open()

	siteTZ := loadSiteTimezones()

	rows, err := db.DB.Query("SELECT booking_id, site_id, start_at, end_at FROM bookings WHERE start_at_utc IS NULL")
	if err != nil {
		log.Fatal(err)
	}
	type row struct{ bookingID, siteID, startAt, endAt string }
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.bookingID, &r.siteID, &r.startAt, &r.endAt); err != nil {
			log.Fatal(err)
		}
		pending = append(pending, r)
	}
	rows.Close()

	fmt.Printf("backfill-booking-utc: %d row(s) need start_at_utc/end_at_utc\n", len(pending))

	succeeded := 0
	var failures []string
	for _, r := range pending {
		loc := siteTZ(r.siteID)
		start, err := availability.ParseBookingTime(r.startAt, loc)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: start_at %q: %v", r.bookingID, r.startAt, err))
			continue
		}
		end, err := availability.ParseBookingTime(r.endAt, loc)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: end_at %q: %v", r.bookingID, r.endAt, err))
			continue
		}
		if *dryRun {
			succeeded++
			continue
		}
		if _, err := db.DB.Exec(
			"UPDATE bookings SET start_at_utc = ?, end_at_utc = ? WHERE booking_id = ?",
			start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), r.bookingID,
		); err != nil {
			failures = append(failures, fmt.Sprintf("%s: UPDATE failed: %v", r.bookingID, err))
			continue
		}
		succeeded++
	}

	fmt.Printf("backfill-booking-utc: %d succeeded, %d failed\n", succeeded, len(failures))
	if len(failures) > 0 {
		fmt.Println("FINDINGS (rows that did not parse - not modified, not silently skipped):")
		for _, f := range failures {
			fmt.Println("  FINDING:", f)
		}
	}
}

func loadSiteTimezones() func(siteID string) *time.Location {
	rows, err := db.DB.Query("SELECT site_id, timezone FROM sites")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	tz := map[string]*time.Location{}
	for rows.Next() {
		var siteID, timezone string
		if err := rows.Scan(&siteID, &timezone); err != nil {
			log.Fatal(err)
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			log.Fatalf("unknown timezone %q for site %q: %v", timezone, siteID, err)
		}
		tz[siteID] = loc
	}
	return func(siteID string) *time.Location {
		if loc, ok := tz[siteID]; ok {
			return loc
		}
		return time.UTC
	}
}

package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"lumen/equipment-api/internal/availability"
	"lumen/equipment-api/internal/db"
	"lumen/equipment-api/internal/fleetcare"
)

// fleetCache holds FleetCare maintenance-window data. Nil until InitFleetCare
// (or InitFleetCareWithFetcher, for tests) is called; see cmd/api/main.go.
var fleetCache *fleetcare.Cache

// InitFleetCare wires a real FleetCare HTTP client into the handler. timeout
// bounds each vendor call; ttl is how often a refresh is attempted; staleAfter
// is how long previously-good data may be served before reading as unknown.
func InitFleetCare(baseURL, apiKey string, timeout, ttl, staleAfter time.Duration) {
	client := fleetcare.NewClient(baseURL, apiKey, timeout)
	fleetCache = fleetcare.NewCache(client, ttl, staleAfter)
}

// InitFleetCareWithFetcher wires a caller-supplied fetcher (a test double,
// typically) instead of a real HTTP client.
func InitFleetCareWithFetcher(fetcher fleetcare.Fetcher, ttl, staleAfter time.Duration) {
	fleetCache = fleetcare.NewCache(fetcher, ttl, staleAfter)
}

// GET /api/equipment/availability?type=mobile_crane&start=2026-10-13&end=2026-10-15
//
// Returns every piece of equipment and whether it is free for the requested dates.
//
// Query-param parsing here is interim (plain "YYYY-MM-DD" or full RFC3339,
// treated as UTC with no required-ness check) and will be hardened with
// proper validation and 400 responses in the "API validation" increment.
// This increment is scoped to overlap/date-window correctness once a start
// and end instant are in hand.
func Availability(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	queryStart, startOK := parseQueryTime(r.URL.Query().Get("start"))
	queryEnd, endOK := parseQueryTime(r.URL.Query().Get("end"))

	query := "SELECT * FROM equipment WHERE status = 'active'"
	args := []any{}
	if typ != "" {
		query += " AND type = ?"
		args = append(args, typ)
	}
	query += " ORDER BY asset_tag"
	rows, err := db.DB.Query(query, args...)
	if err != nil {
		panic(err)
	}
	equipment := scanAll(rows)

	equipmentTags := make([]string, 0, len(equipment))
	for _, item := range equipment {
		equipmentTags = append(equipmentTags, asString(item["asset_tag"]))
	}

	bookings := loadAllBookings()
	index, tagWarnings := availability.BuildEquipmentIndex(bookings, equipmentTags)
	for _, w := range tagWarnings {
		log.Printf("availability: data quality warning: booking %s: %s", w.BookingID, w.Reason)
	}

	siteTZ := loadSiteTimezones()

	if fleetCache != nil && startOK && endOK {
		fleetCache.EnsureFresh(r.Context(), equipmentTags, time.Now())
	}

	results := []map[string]any{}
	for _, item := range equipment {
		tag := asString(item["asset_tag"])
		key := availability.NormalizeTag(tag)

		var conflictOut map[string]any
		bookingAvailable := true

		if startOK && endOK {
			conflict, warnings := availability.FindConflict(index[key], siteTZ, queryStart, queryEnd)
			for _, w := range warnings {
				log.Printf("availability: data quality warning: booking %s: %s", w.BookingID, w.Reason)
			}
			if conflict != nil {
				bookingAvailable = false
				conflictOut = map[string]any{
					"booking_id": conflict.BookingID,
					"site_id":    conflict.SiteID,
					"start_at":   conflict.StartAt,
					"end_at":     conflict.EndAt,
					"status":     conflict.Status,
				}
				if conflict.OperatorID != "" {
					orows, _ := db.DB.Query("SELECT * FROM operators WHERE operator_id = ?", conflict.OperatorID)
					ops := scanAll(orows)
					if len(ops) > 0 {
						conflictOut["operator"] = ops[0]
					}
				}
			}
		}

		// maintenanceStatus is "unknown" whenever fleetCache isn't wired up or
		// the query window is unparsed - never silently "ok". available is a
		// function of BOTH signals, not just bookings: this is the fix for the
		// seam a prior review flagged as the one thing that could still make
		// Dana's complaint ("looks free, isn't") recur through FleetCare
		// specifically. No consumer can forget to check maintenance.status
		// separately, because it's already folded into the one boolean.
		maintenanceStatus := "unknown"
		if fleetCache != nil && startOK && endOK {
			maintenanceStatus, _ = fleetCache.Status(tag, time.Now(), queryStart, queryEnd)
		}
		availableOut := bookingAvailable && maintenanceStatus == "ok"

		item["available"] = availableOut
		item["conflict"] = conflictOut
		item["maintenance"] = map[string]any{"status": maintenanceStatus}
		results = append(results, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": results})
}

// parseQueryTime accepts a full RFC3339 timestamp or a plain "YYYY-MM-DD"
// date (treated as UTC midnight). Interim, pending the validation increment.
func parseQueryTime(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func loadAllBookings() []availability.Booking {
	rows, err := db.DB.Query("SELECT booking_id, asset_tag, site_id, start_at, end_at, operator_id, status FROM bookings ORDER BY booking_id")
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	var out []availability.Booking
	for rows.Next() {
		var b availability.Booking
		var operatorID sql.NullString
		if err := rows.Scan(&b.BookingID, &b.AssetTag, &b.SiteID, &b.StartAt, &b.EndAt, &operatorID, &b.Status); err != nil {
			panic(err)
		}
		b.OperatorID = operatorID.String
		out = append(out, b)
	}
	return out
}

func loadSiteTimezones() func(siteID string) *time.Location {
	rows, err := db.DB.Query("SELECT site_id, timezone FROM sites")
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	tz := map[string]*time.Location{}
	for rows.Next() {
		var siteID, timezone string
		if err := rows.Scan(&siteID, &timezone); err != nil {
			panic(err)
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			log.Printf("availability: unknown timezone %q for site %q, defaulting to UTC", timezone, siteID)
			loc = time.UTC
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

// scanAll turns rows into a slice of column->value maps.
func scanAll(rows *sql.Rows) []map[string]any {
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rows.Scan(ptrs...)
		m := map[string]any{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out
}

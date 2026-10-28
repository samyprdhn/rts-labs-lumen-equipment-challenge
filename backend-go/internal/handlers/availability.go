package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"lumen/equipment-api/internal/db"
)

// GET /api/equipment/availability?type=mobile_crane&start=2026-10-13&end=2026-10-15
//
// Returns every piece of equipment and whether it is free for the requested dates.
func Availability(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")

	query := "SELECT * FROM equipment WHERE status = 'active'"
	if typ != "" {
		query += " AND type = '" + typ + "'"
	}
	rows, err := db.DB.Query(query)
	if err != nil {
		panic(err)
	}
	equipment := scanAll(rows)

	results := []map[string]any{}
	for _, item := range equipment {
		brows, err := db.DB.Query("SELECT * FROM bookings WHERE asset_tag = ?", item["asset_tag"])
		if err != nil {
			panic(err)
		}
		bookings := scanAll(brows)

		available := true
		var conflict map[string]any
		for _, b := range bookings {
			if b["start_at"].(string) >= start && b["end_at"].(string) <= end {
				available = false
				conflict = b
				if b["operator_id"] != "" {
					orows, _ := db.DB.Query("SELECT * FROM operators WHERE operator_id = ?", b["operator_id"])
					ops := scanAll(orows)
					if len(ops) > 0 {
						conflict["operator"] = ops[0]
					}
				}
			}
		}

		// TODO: check FleetCare for maintenance windows (Dana says this is the #1 complaint)

		item["available"] = available
		item["conflict"] = conflict
		results = append(results, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": results})
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

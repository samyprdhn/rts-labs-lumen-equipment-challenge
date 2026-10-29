package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	sqlite3 "modernc.org/sqlite"
	sqlite3lib "modernc.org/sqlite/lib"

	"lumen/equipment-api/internal/auth"
	"lumen/equipment-api/internal/availability"
	"lumen/equipment-api/internal/certs"
	"lumen/equipment-api/internal/db"
)

// This file implements only POST /api/reservations (docs/plan/02-reservations.md,
// docs/decisions/0002-reservation-concurrency-control.md). The confirm and
// release endpoints, and the idempotency-key table (plan tasks 4 and 5), are
// approved in design but deliberately NOT built here - a separate pass, per
// instruction. A reservation created here can be "tentative" or "confirmed"
// (per the approved lifecycle), but nothing in this file can transition one
// afterward.

type createReservationRequest struct {
	AssetTag   string `json:"asset_tag"`
	SiteID     string `json:"site_id"`
	Start      string `json:"start"`
	End        string `json:"end"`
	Status     string `json:"status"`
	OperatorID string `json:"operator_id"`
	ExpiresAt  string `json:"expires_at"`
}

// POST /api/reservations
//
// Order: decode/validate request shape -> authorize (B1) -> resolve
// equipment/site/operator and check certification (B3/B4) -> only then open
// the write transaction that holds the overlap check and the INSERT
// together. Everything before the transaction is read-only and does not
// need the write lock; only the check-then-insert sequence does.
func CreateReservation(w http.ResponseWriter, r *http.Request) {
	var req createReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "malformed JSON body")
		return
	}

	if req.AssetTag == "" || req.SiteID == "" || req.Start == "" || req.End == "" || req.Status == "" {
		writeError(w, http.StatusBadRequest, "asset_tag, site_id, start, end, and status are required")
		return
	}

	start, err := time.Parse(time.RFC3339, req.Start)
	if err != nil {
		writeError(w, http.StatusBadRequest, "start must be an RFC3339 timestamp")
		return
	}
	end, err := time.Parse(time.RFC3339, req.End)
	if err != nil {
		writeError(w, http.StatusBadRequest, "end must be an RFC3339 timestamp")
		return
	}
	if !end.After(start) {
		writeError(w, http.StatusBadRequest, "end must be after start")
		return
	}

	if req.Status != "tentative" && req.Status != "confirmed" {
		writeError(w, http.StatusBadRequest, `status must be "tentative" or "confirmed"`)
		return
	}

	var expiresAt time.Time
	if req.Status == "tentative" {
		// Point 3 of the approved tentative-hold policy: every hold has a
		// required expiration, not an optional one.
		if req.ExpiresAt == "" {
			writeError(w, http.StatusBadRequest, `expires_at is required when status is "tentative"`)
			return
		}
		expiresAt, err = time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "expires_at must be an RFC3339 timestamp")
			return
		}
		if !expiresAt.After(time.Now()) {
			writeError(w, http.StatusBadRequest, "expires_at must be in the future")
			return
		}
	} else if req.ExpiresAt != "" {
		writeError(w, http.StatusBadRequest, `expires_at must not be set when status is "confirmed"`)
		return
	}

	user := auth.UserFrom(r)
	if user == nil {
		// RequireAuth already guarantees this; defensive only.
		writeError(w, http.StatusUnauthorized, "invalid or missing token")
		return
	}

	// B1: ops_director may book anywhere; superintendent/foreman only at a
	// site in their own site_ids; everyone else (subcontractor,
	// safety_manager) cannot create a reservation at all. Unlike Part 1's
	// read path, there's no partial/redacted response for a write - it's a
	// clean 403.
	authorized := false
	switch user.Role {
	case "ops_director":
		authorized = true
	case "superintendent", "foreman":
		authorized = user.CanAccessSite(req.SiteID)
	}
	if !authorized {
		writeError(w, http.StatusForbidden, fmt.Sprintf("not authorized to book at site %s", req.SiteID))
		return
	}

	equipment, err := loadEquipment(req.AssetTag)
	if err != nil {
		log.Printf("reservations: loading equipment: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if equipment == nil || equipment.Status != "active" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown or inactive asset_tag %q", req.AssetTag))
		return
	}

	site, err := loadSite(req.SiteID)
	if err != nil {
		log.Printf("reservations: loading site: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if site == nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown site_id %q", req.SiteID))
		return
	}

	// B4: cert-required equipment needs a real operator_id; equipment with
	// no required_cert does not. An operator_id given for non-cert-required
	// equipment is still validated against the operators table if present,
	// so a typo'd operator_id is never silently accepted.
	if equipment.RequiredCert != "" && req.OperatorID == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("operator_id is required for equipment requiring %s", equipment.RequiredCert))
		return
	}
	if req.OperatorID != "" {
		operator, err := loadOperator(req.OperatorID)
		if err != nil {
			log.Printf("reservations: loading operator: %v", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if operator == nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown operator_id %q", req.OperatorID))
			return
		}
		if equipment.RequiredCert != "" {
			// B3: evaluated against the booking's end, in the booking
			// site's timezone - a cert good "through" the start isn't
			// enough if it lapses mid-booking.
			loc := loadSiteTimezones()(req.SiteID)
			held, parseErrs := certs.ParseCertifications(operator.Certifications, loc)
			for _, e := range parseErrs {
				log.Printf("reservations: operator %s: %v", req.OperatorID, e)
			}
			if !certs.HasValidCert(held, equipment.RequiredCert, end) {
				hasAny := false
				for _, c := range held {
					if c.Name == equipment.RequiredCert {
						hasAny = true
					}
				}
				if hasAny {
					writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("operator %s's %s certification expires before the booking ends", req.OperatorID, equipment.RequiredCert))
				} else {
					writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("operator %s does not hold the required %s certification", req.OperatorID, equipment.RequiredCert))
				}
				return
			}
		}
	}

	bookingID, err := generateBookingID()
	if err != nil {
		log.Printf("reservations: generating booking id: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	startUTC := start.UTC().Format(time.RFC3339)
	endUTC := end.UTC().Format(time.RFC3339)
	nowUTC := time.Now().UTC()
	var expiresAtUTC sql.NullString
	if req.Status == "tentative" {
		expiresAtUTC = sql.NullString{String: expiresAt.UTC().Format(time.RFC3339), Valid: true}
	}
	var operatorIDVal sql.NullString
	if req.OperatorID != "" {
		operatorIDVal = sql.NullString{String: req.OperatorID, Valid: true}
	}

	ctx := r.Context()
	conn, err := db.DB.Conn(ctx)
	if err != nil {
		log.Printf("reservations: acquiring connection: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer conn.Close()

	// BEGIN IMMEDIATE acquires SQLite's write lock up front (not lazily on
	// first write) - see ADR 0002. Everything from here to COMMIT/ROLLBACK
	// runs on this one raw connection, never database/sql's Tx wrapper, so
	// the busy/trigger error codes surface exactly as the ADR's experiments
	// observed them.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if isBusy(err) {
			writeBusy(w)
			return
		}
		log.Printf("reservations: BEGIN IMMEDIATE: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	committed := false
	defer func() {
		if !committed {
			if _, rbErr := conn.ExecContext(context.Background(), "ROLLBACK"); rbErr != nil {
				log.Printf("reservations: rollback: %v", rbErr)
			}
		}
	}()

	conflict, err := findActiveConflict(ctx, conn, equipment.AssetTag, startUTC, endUTC)
	if err != nil {
		log.Printf("reservations: overlap pre-check: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if conflict != nil {
		writeConflictResponse(w, user, conflict)
		return
	}

	_, err = conn.ExecContext(ctx,
		`INSERT INTO bookings (booking_id, asset_tag, site_id, start_at, end_at, operator_id, booked_by, status, notes, start_at_utc, end_at_utc, expires_at_utc)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bookingID, equipment.AssetTag, req.SiteID,
		startUTC, endUTC,
		operatorIDVal, user.Sub, req.Status, sql.NullString{},
		startUTC, endUTC, expiresAtUTC,
	)
	if err != nil {
		var sqliteErr *sqlite3.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3lib.SQLITE_CONSTRAINT_TRIGGER {
			// The app-level pre-check above found nothing, but the trigger -
			// which runs inside this same transaction, against the same
			// committed state - disagrees. Since BEGIN IMMEDIATE has held
			// the write lock since before the pre-check, nothing else could
			// have written in between; this is the trigger backstop doing
			// its job against a gap in the pre-check logic itself, which is
			// worth knowing about, not just silently absorbing as a normal
			// conflict.
			log.Printf("reservations: trigger rejected an insert the app-level pre-check missed: asset_tag=%s", equipment.AssetTag)
			writeError(w, http.StatusConflict, "conflict")
			return
		}
		if isBusy(err) {
			writeBusy(w)
			return
		}
		log.Printf("reservations: insert failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		if isBusy(err) {
			writeBusy(w)
			return
		}
		log.Printf("reservations: commit failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	committed = true

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
		"booking_id":  bookingID,
		"asset_tag":   equipment.AssetTag,
		"site_id":     req.SiteID,
		"start_at":    startUTC,
		"end_at":      endUTC,
		"status":      req.Status,
		"operator_id": nullableString(operatorIDVal),
		"expires_at":  nullableString(expiresAtUTC),
		"booked_by":   user.Sub,
		"created_at":  nowUTC.Format(time.RFC3339),
	}})
}

// findActiveConflict runs, inside the caller's transaction, the exact same
// overlap predicate as the no_overlapping_active_bookings trigger (schema.sql)
// against the new booking's canonical UTC window - so a real conflict is
// reported here, with detail, before ever attempting the INSERT whose own
// rejection (by design) carries none.
func findActiveConflict(ctx context.Context, conn *sql.Conn, assetTag, startUTC, endUTC string) (*availability.Conflict, error) {
	row := conn.QueryRowContext(ctx, `
		SELECT booking_id, site_id, start_at, end_at, status, operator_id
		FROM bookings
		WHERE asset_tag = ?
		  AND (
		    status = 'confirmed'
		    OR (status = 'tentative' AND (expires_at_utc IS NULL OR expires_at_utc > datetime('now')))
		  )
		  AND ? < end_at_utc
		  AND start_at_utc < ?
		LIMIT 1`,
		assetTag, startUTC, endUTC,
	)
	var c availability.Conflict
	var operatorID sql.NullString
	err := row.Scan(&c.BookingID, &c.SiteID, &c.StartAt, &c.EndAt, &c.Status, &operatorID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.OperatorID = operatorID.String
	return &c, nil
}

// writeConflictResponse applies Part 1's exact redaction rule (fullDetailRole /
// siteMatchedRole / hasSite, availability.go) to a write-path conflict, so a
// caller never learns more about someone else's booking via a 409 than they
// would have via the read endpoint's conflict detail.
func writeConflictResponse(w http.ResponseWriter, user *auth.User, c *availability.Conflict) {
	var conflictOut map[string]any
	if user != nil && (fullDetailRole(user.Role) || (siteMatchedRole(user.Role) && hasSite(user.SiteIDs, c.SiteID))) {
		conflictOut = map[string]any{
			"booking_id": c.BookingID,
			"site_id":    c.SiteID,
			"start_at":   c.StartAt,
			"end_at":     c.EndAt,
			"status":     c.Status,
		}
	} else {
		conflictOut = map[string]any{"exists": true}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(map[string]any{"error": "conflict", "conflict": conflictOut})
}

func isBusy(err error) bool {
	var sqliteErr *sqlite3.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3lib.SQLITE_BUSY
}

func writeBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusServiceUnavailable, "try again")
}

func nullableString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func generateBookingID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "RES-" + hex.EncodeToString(buf), nil
}

type equipmentRow struct {
	AssetTag     string
	Status       string
	RequiredCert string
}

// loadEquipment resolves a client-supplied asset_tag the same way Part 1
// resolves one from the messier bookings.csv data: via NormalizeTag, not an
// exact string match (docs/plan/02-reservations.md's validation section
// calls for reusing it explicitly). The equipment table is small (tens of
// rows), so a full scan here is simpler and clearer than pushing a
// normalization expression into SQL.
func loadEquipment(rawAssetTag string) (*equipmentRow, error) {
	key := availability.NormalizeTag(rawAssetTag)
	rows, err := db.DB.Query("SELECT asset_tag, status, required_cert FROM equipment")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e equipmentRow
		if err := rows.Scan(&e.AssetTag, &e.Status, &e.RequiredCert); err != nil {
			return nil, err
		}
		if availability.NormalizeTag(e.AssetTag) == key {
			return &e, nil
		}
	}
	return nil, rows.Err()
}

type siteRow struct {
	SiteID string
}

func loadSite(siteID string) (*siteRow, error) {
	row := db.DB.QueryRow("SELECT site_id FROM sites WHERE site_id = ?", siteID)
	var s siteRow
	err := row.Scan(&s.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

type operatorRow struct {
	OperatorID     string
	Certifications string
}

func loadOperator(operatorID string) (*operatorRow, error) {
	row := db.DB.QueryRow("SELECT operator_id, certifications FROM operators WHERE operator_id = ?", operatorID)
	var o operatorRow
	err := row.Scan(&o.OperatorID, &o.Certifications)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

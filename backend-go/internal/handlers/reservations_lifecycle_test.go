package handlers_test

// Enumerated before writing any test code (write-tests skill, step 1).
// Reuses reservations_test.go's helpers (insertTestEquipment/Site/Operator/
// Booking, tokenFor, nextTestID, rfc3339, farFutureBase) - same package,
// same disposable-DB TestMain.
//
// | # | Scenario | Expected | Why it matters |
// |---|---|---|---|
// | 1  | Confirm a valid tentative hold | 200, status=confirmed | happy path |
// | 2  | Release a tentative hold | 200, status=cancelled | happy path |
// | 3  | Release a confirmed booking | 200, status=cancelled | the scope generalization this plan adds |
// | 4  | Confirm/release a nonexistent booking_id | 404 for both | invalid input |
// | 5  | Confirm/release by an unrelated role/site | 403 for both | B6 |
// | 6  | Confirm/release by the original requester, even with a role the B1 switch alone would reject | 200 for both | B6's "original requester" clause bypasses the role check entirely |
// | 7  | Confirm/release by ops_director, not the requester | 200 for both | B6 |
// | 8  | Confirm/release by superintendent/foreman at the booking's own site, not the requester | 200 for both | B6 |
// | 9  | Confirm/release by superintendent/foreman at a DIFFERENT site, not the requester | 403 for both | B6 |
// | 10 | Confirm a booking that's already confirmed | 409 | idempotency/state guard |
// | 11 | Confirm a booking that's already cancelled | 409 | idempotency/state guard |
// | 12 | Confirm an EXPIRED tentative hold | 409 "hold has expired" | the trigger's first RAISE |
// | 13 | Confirm a valid hold that now overlaps a competing confirmed booking | 409 (defense-in-depth) | the trigger's second RAISE |
// | 14 | Release an already-cancelled booking | 409 | idempotency/state guard |
// | 15 | Boundary: confirm exactly at expires_at_utc == now | rejected as expired | trigger's inclusive `<=` |
// | 16 | Boundary: confirm 1s before expiry | succeeds | same boundary, other side |
// | 17 | No token | 401 for both | existing middleware |
// | 18 | Successful confirm/release response includes the full reservation body | asset_tag/site_id/booked_by/operator_id present | response contract |
//
// Concurrency (real-process, real-HTTP) is covered separately in
// backend-go/integration/, per the Part 3A precedent already set for create.

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"lumen/equipment-api/internal/db"
	"lumen/equipment-api/internal/server"
)

func postAction(path string, bearer string) (int, map[string]any) {
	req := httptest.NewRequest("POST", path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	server.New().ServeHTTP(rec, req)
	var parsed map[string]any
	if rec.Body.Len() > 0 {
		json.Unmarshal(rec.Body.Bytes(), &parsed)
	}
	return rec.Code, parsed
}

func confirmPath(id string) string { return "/api/reservations/" + id + "/confirm" }
func releasePath(id string) string { return "/api/reservations/" + id + "/release" }

// setupBooking inserts a fresh equipment/site and one booking row directly,
// returning the booking_id. bookedBy sets the row's booked_by column
// directly (bypassing the create endpoint), so lifecycle tests can exercise
// B6's owner-match branch independently of who is allowed to create.
func setupBooking(t *testing.T, status string, bookedBy string, expiresAtUTC string) (bookingID, asset, site string) {
	t.Helper()
	asset = "ZT" + nextTestID()
	site = "ZS" + nextTestID()
	bookingID = "ZB" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")
	start := farFutureBase
	end := farFutureBase.Add(8 * time.Hour)
	var exp any
	if expiresAtUTC != "" {
		exp = expiresAtUTC
	}
	_, err := db.DB.Exec(
		`INSERT INTO bookings (booking_id, asset_tag, site_id, start_at, end_at, status, booked_by, start_at_utc, end_at_utc, expires_at_utc)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bookingID, asset, site, rfc3339(start), rfc3339(end), status, bookedBy, rfc3339(start), rfc3339(end), exp,
	)
	if err != nil {
		t.Fatalf("setupBooking: %v", err)
	}
	return bookingID, asset, site
}

func TestConfirmReservation_HappyPath(t *testing.T) {
	id, _, _ := setupBooking(t, "tentative", "u-test-ops_director", rfc3339(time.Now().Add(time.Hour)))
	code, body := postAction(confirmPath(id), tokenFor("ops_director", []string{"*"}))
	if code != 200 {
		t.Fatalf("expected 200, got %d: %+v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["status"] != "confirmed" {
		t.Fatalf("expected status confirmed, got %+v", data)
	}
}

func TestReleaseReservation_HappyPath_FromTentative(t *testing.T) {
	id, _, _ := setupBooking(t, "tentative", "u-test-ops_director", rfc3339(time.Now().Add(time.Hour)))
	code, body := postAction(releasePath(id), tokenFor("ops_director", []string{"*"}))
	if code != 200 {
		t.Fatalf("expected 200, got %d: %+v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["status"] != "cancelled" {
		t.Fatalf("expected status cancelled, got %+v", data)
	}
}

func TestReleaseReservation_HappyPath_FromConfirmed(t *testing.T) {
	id, _, _ := setupBooking(t, "confirmed", "u-test-ops_director", "")
	code, body := postAction(releasePath(id), tokenFor("ops_director", []string{"*"}))
	if code != 200 {
		t.Fatalf("expected 200 (release must work from confirmed too), got %d: %+v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["status"] != "cancelled" {
		t.Fatalf("expected status cancelled, got %+v", data)
	}
}

func TestConfirmRelease_NonexistentBookingID(t *testing.T) {
	tok := tokenFor("ops_director", []string{"*"})
	missing := "ZB-does-not-exist-" + nextTestID()
	t.Run("confirm", func(t *testing.T) {
		code, body := postAction(confirmPath(missing), tok)
		if code != 404 {
			t.Fatalf("expected 404, got %d: %+v", code, body)
		}
	})
	t.Run("release", func(t *testing.T) {
		code, body := postAction(releasePath(missing), tok)
		if code != 404 {
			t.Fatalf("expected 404, got %d: %+v", code, body)
		}
	})
}

func TestConfirmRelease_NoToken(t *testing.T) {
	id, _, _ := setupBooking(t, "tentative", "u-test-ops_director", rfc3339(time.Now().Add(time.Hour)))
	t.Run("confirm", func(t *testing.T) {
		code, _ := postAction(confirmPath(id), "")
		if code != 401 {
			t.Fatalf("expected 401, got %d", code)
		}
	})
	t.Run("release", func(t *testing.T) {
		code, _ := postAction(releasePath(id), "")
		if code != 401 {
			t.Fatalf("expected 401, got %d", code)
		}
	})
}

func TestConfirmRelease_Authorization(t *testing.T) {
	siteOwn := "ZS" + nextTestID()
	siteOther := "ZS" + nextTestID()
	insertTestSite(t, siteOwn, "America/New_York")
	insertTestSite(t, siteOther, "America/New_York")

	newBooking := func(t *testing.T, status, bookedBy, site string) string {
		t.Helper()
		asset := "ZT" + nextTestID()
		bookingID := "ZB" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		start := farFutureBase
		end := farFutureBase.Add(8 * time.Hour)
		var exp any
		if status == "tentative" {
			exp = rfc3339(time.Now().Add(time.Hour))
		}
		_, err := db.DB.Exec(
			`INSERT INTO bookings (booking_id, asset_tag, site_id, start_at, end_at, status, booked_by, start_at_utc, end_at_utc, expires_at_utc)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			bookingID, asset, site, rfc3339(start), rfc3339(end), status, bookedBy, rfc3339(start), rfc3339(end), exp,
		)
		if err != nil {
			t.Fatalf("newBooking: %v", err)
		}
		return bookingID
	}

	cases := []struct {
		name     string
		bookedBy string
		role     string
		siteIDs  []string
		wantCode int
	}{
		{"unrelated_role_and_site_forbidden", "someone-else", "subcontractor", []string{siteOwn}, 403},
		{"wrong_site_authority_forbidden", "someone-else", "superintendent", []string{siteOther}, 403},
		{"original_requester_allowed_even_as_subcontractor", "u-test-subcontractor", "subcontractor", []string{siteOwn}, 200},
		{"ops_director_not_requester_allowed", "someone-else", "ops_director", []string{"*"}, 200},
		{"superintendent_same_site_not_requester_allowed", "someone-else", "superintendent", []string{siteOwn}, 200},
		{"foreman_same_site_not_requester_allowed", "someone-else", "foreman", []string{siteOwn}, 200},
	}
	for _, c := range cases {
		t.Run("confirm_"+c.name, func(t *testing.T) {
			id := newBooking(t, "tentative", c.bookedBy, siteOwn)
			code, body := postAction(confirmPath(id), tokenFor(c.role, c.siteIDs))
			if code != c.wantCode {
				t.Fatalf("expected %d, got %d: %+v", c.wantCode, code, body)
			}
		})
		t.Run("release_"+c.name, func(t *testing.T) {
			id := newBooking(t, "confirmed", c.bookedBy, siteOwn)
			code, body := postAction(releasePath(id), tokenFor(c.role, c.siteIDs))
			if code != c.wantCode {
				t.Fatalf("expected %d, got %d: %+v", c.wantCode, code, body)
			}
		})
	}
}

func TestConfirmReservation_StateConflicts(t *testing.T) {
	tok := tokenFor("ops_director", []string{"*"})

	t.Run("already_confirmed", func(t *testing.T) {
		id, _, _ := setupBooking(t, "confirmed", "u-test-ops_director", "")
		code, body := postAction(confirmPath(id), tok)
		if code != 409 {
			t.Fatalf("expected 409, got %d: %+v", code, body)
		}
	})

	t.Run("already_cancelled", func(t *testing.T) {
		id, _, _ := setupBooking(t, "cancelled", "u-test-ops_director", "")
		code, body := postAction(confirmPath(id), tok)
		if code != 409 {
			t.Fatalf("expected 409, got %d: %+v", code, body)
		}
	})

	t.Run("expired_hold_rejected_with_specific_message", func(t *testing.T) {
		id, _, _ := setupBooking(t, "tentative", "u-test-ops_director", rfc3339(time.Now().Add(-time.Hour)))
		code, body := postAction(confirmPath(id), tok)
		if code != 409 {
			t.Fatalf("expected 409, got %d: %+v", code, body)
		}
		if body["error"] != "hold has expired" {
			t.Fatalf(`expected error "hold has expired", got %+v`, body)
		}
	})

	t.Run("overlapping_competitor_rejected_defense_in_depth", func(t *testing.T) {
		// Simulate a pre-existing competitor without going through the
		// INSERT trigger (which would normally and correctly prevent this
		// exact situation from arising live) - deliberately testing the
		// no_overlap_on_confirm trigger's second RAISE in isolation, same
		// methodology used to verify it empirically before this test was
		// written (docs/plan/03-confirm-release.md).
		asset := "ZT" + nextTestID()
		site := "ZS" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestSite(t, site, "America/New_York")
		start := farFutureBase
		end := farFutureBase.Add(8 * time.Hour)

		id := "ZB" + nextTestID()
		_, err := db.DB.Exec(
			`INSERT INTO bookings (booking_id, asset_tag, site_id, start_at, end_at, status, booked_by, start_at_utc, end_at_utc, expires_at_utc)
			 VALUES (?, ?, ?, ?, ?, 'tentative', ?, ?, ?, ?)`,
			id, asset, site, rfc3339(start), rfc3339(end), "u-test-ops_director", rfc3339(start), rfc3339(end), rfc3339(time.Now().Add(time.Hour)),
		)
		if err != nil {
			t.Fatalf("insert hold: %v", err)
		}

		competitorID := "ZB" + nextTestID()
		if _, err := db.DB.Exec(
			`INSERT INTO bookings (booking_id, asset_tag, site_id, start_at, end_at, status, start_at_utc, end_at_utc)
			 VALUES (?, ?, ?, ?, ?, 'cancelled', ?, ?)`,
			competitorID, asset, site, rfc3339(start.Add(time.Hour)), rfc3339(end.Add(-time.Hour)), rfc3339(start.Add(time.Hour)), rfc3339(end.Add(-time.Hour)),
		); err != nil {
			t.Fatalf("insert competitor: %v", err)
		}
		if _, err := db.DB.Exec("UPDATE bookings SET status='confirmed' WHERE booking_id=?", competitorID); err != nil {
			t.Fatalf("promote competitor to confirmed: %v", err)
		}

		code, body := postAction(confirmPath(id), tok)
		if code != 409 {
			t.Fatalf("expected 409, got %d: %+v", code, body)
		}
	})
}

func TestReleaseReservation_AlreadyCancelled(t *testing.T) {
	id, _, _ := setupBooking(t, "cancelled", "u-test-ops_director", "")
	code, body := postAction(releasePath(id), tokenFor("ops_director", []string{"*"}))
	if code != 409 {
		t.Fatalf("expected 409, got %d: %+v", code, body)
	}
}

func TestConfirmReservation_ExpiryBoundary(t *testing.T) {
	tok := tokenFor("ops_director", []string{"*"})

	t.Run("exactly_at_expiry_is_rejected", func(t *testing.T) {
		id, _, _ := setupBooking(t, "tentative", "u-test-ops_director", rfc3339(time.Now()))
		code, body := postAction(confirmPath(id), tok)
		if code != 409 {
			t.Fatalf("expected 409 (expires_at_utc == now counts as expired), got %d: %+v", code, body)
		}
	})

	t.Run("one_second_before_expiry_succeeds", func(t *testing.T) {
		id, _, _ := setupBooking(t, "tentative", "u-test-ops_director", rfc3339(time.Now().Add(time.Second)))
		code, body := postAction(confirmPath(id), tok)
		if code != 200 {
			t.Fatalf("expected 200, got %d: %+v", code, body)
		}
	})
}

func TestConfirmReservation_ResponseBody(t *testing.T) {
	id, asset, site := setupBooking(t, "tentative", "u-test-ops_director", rfc3339(time.Now().Add(time.Hour)))
	code, body := postAction(confirmPath(id), tokenFor("ops_director", []string{"*"}))
	if code != 200 {
		t.Fatalf("expected 200, got %d: %+v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["booking_id"] != id || data["asset_tag"] != asset || data["site_id"] != site || data["booked_by"] != "u-test-ops_director" {
		t.Fatalf("expected full reservation body, got %+v", data)
	}
}

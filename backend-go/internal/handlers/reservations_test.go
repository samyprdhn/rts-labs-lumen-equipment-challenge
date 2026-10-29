package handlers_test

// Enumerated before writing any test code (write-tests skill, step 1).
//
// | # | Scenario | Setup | Expected | Why it matters |
// |---|---|---|---|---|
// | 1  | Happy path, confirmed, no cert required | fresh equipment/site, status=confirmed | 201 | core path |
// | 2  | Happy path, confirmed, cert required, operator holds a valid cert | | 201 | core path + cert |
// | 3  | Happy path, tentative with a future expires_at | | 201, expires_at echoed | tentative creation |
// | 4-8| Missing each required field (asset_tag/site_id/start/end/status) | omit one at a time | 400 | invalid input |
// | 9  | end before start | end < start | 400 | boundary |
// | 10 | end equals start (zero-length) | end == start | 400 | boundary |
// | 11 | status is neither tentative nor confirmed | status="cancelled" | 400 | invalid input |
// | 12 | tentative with no expires_at | | 400 | B2: expiry required |
// | 13 | tentative with expires_at in the past | | 400 | sanity boundary |
// | 14 | confirmed with expires_at set | | 400 | strict contract |
// | 15 | start/end not RFC3339 | start="not-a-date" | 400 | invalid input |
// | 16 | unknown asset_tag | | 400 | invalid input |
// | 17 | inactive (retired) equipment | equipment.status="retired" | 400 | invalid input |
// | 18 | asset_tag resolved via NormalizeTag (dirty case/whitespace) | request "zt-77 " vs stored "ZT77" | 201 | reuses Part 1's normalizer |
// | 19 | unknown site_id | | 400 | invalid input |
// | 20 | cert-required equipment, operator_id missing | | 400 | B4 |
// | 21 | cert-required equipment, unknown operator_id | | 400 | invalid input |
// | 22 | cert-required equipment, operator holds no matching cert | | 422 | B3/B4 |
// | 23 | cert-required equipment, operator's cert expires before booking end | | 422 | B3 boundary |
// | 24 | cert-required equipment, operator's cert expires exactly at booking end | | 201 | B3 boundary, inclusive |
// | 25 | non-cert equipment, operator_id omitted | | 201 | B4: not required |
// | 26 | non-cert equipment, operator_id given but unknown | | 400 | still validated if present |
// | 27 | ops_director books a site outside their own site_ids | | 201 | B1 |
// | 28 | superintendent books their own site | | 201 | B1 |
// | 29 | superintendent books a site NOT in their site_ids | | 403 | B1 |
// | 30 | foreman: same own/other-site split as superintendent | | 201 / 403 | B1 |
// | 31 | subcontractor, any site | | 403 | B1 |
// | 32 | safety_manager, any site, despite site_ids=["*"] | | 403 | B1 explicit carve-out |
// | 33 | no bearer token | | 401 | existing middleware |
// | 34 | exact duplicate window, same asset | existing confirmed row | 409 | core invariant |
// | 35 | new request's start falls inside an existing booking | | 409 | overlap geometry |
// | 36 | new request's end falls inside an existing booking | | 409 | overlap geometry |
// | 37 | new request fully contains an existing booking | | 409 | overlap geometry |
// | 38 | existing booking fully contains the new request | | 409 | overlap geometry |
// | 39 | adjacent, non-overlapping (new starts exactly when existing ends) | | 201 | strict-inequality boundary |
// | 40 | disjoint window, same asset | | 201 | required explicit case |
// | 41 | overlapping window, different asset | | 201 | required explicit case |
// | 42 | existing booking is cancelled, overlapping window | | 201 | cancelled never blocks |
// | 43 | existing tentative hold, not yet expired, overlapping window | | 409 | B2: tentative blocks |
// | 44 | existing tentative hold, already expired, overlapping window | | 201 | B2: expired tentative doesn't block |
// | 45 | conflict redaction: ops_director sees full conflict detail | | 409 + full detail | reuses Part 1 redaction |
// | 46 | conflict redaction: foreman sees full detail for a conflict at their OWN site | | 409 + full detail | reuses Part 1 redaction |
// | 47 | conflict redaction: foreman sees redacted detail for a conflict whose site isn't theirs (asset moved) | | 409 + {"exists":true} | reuses Part 1 redaction |
// | 48 | booked_by is taken from the verified JWT sub, not the request body | body includes a spoofed booked_by | response booked_by == JWT sub | can't forge audit field |
// | 49 | response never includes operator PII (name/phone/ssn), only operator_id | | no PII keys present | security |
// | 50 | asset_tag containing SQL-metacharacters is treated as plain unknown input | asset_tag="x'); DROP TABLE bookings;--" | 400, no error, table intact | parameterized queries |
//
// Concurrency (deterministic, real HTTP, disposable DB) is its own test
// file per the instruction's own structure, not duplicated here.
// Data-quality (full historical reseed) was already verified separately
// against a disposable DB during the schema-migration step.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lumen/equipment-api/internal/db"
	"lumen/equipment-api/internal/server"
)

var reservationTestCounter int

// nextTestID returns a short, monotonically increasing suffix so each test
// can build its own fresh, collision-free equipment/site/operator/booking
// fixtures instead of depending on whatever happens to already be in the
// seeded data (write-tests: "tests that depend on whatever happens to be in
// the dev database are brittle"). Safe without synchronization because
// these tests don't run with t.Parallel().
func nextTestID() string {
	reservationTestCounter++
	return fmt.Sprintf("%d", reservationTestCounter)
}

func insertTestEquipment(t *testing.T, assetTag, status, requiredCert string) {
	t.Helper()
	if _, err := db.DB.Exec("INSERT INTO equipment (asset_tag, status, required_cert) VALUES (?, ?, ?)", assetTag, status, requiredCert); err != nil {
		t.Fatalf("insertTestEquipment(%s): %v", assetTag, err)
	}
}

func insertTestSite(t *testing.T, siteID, timezone string) {
	t.Helper()
	if _, err := db.DB.Exec("INSERT INTO sites (site_id, timezone) VALUES (?, ?)", siteID, timezone); err != nil {
		t.Fatalf("insertTestSite(%s): %v", siteID, err)
	}
}

func insertTestOperator(t *testing.T, operatorID, certifications string) {
	t.Helper()
	if _, err := db.DB.Exec("INSERT INTO operators (operator_id, certifications) VALUES (?, ?)", operatorID, certifications); err != nil {
		t.Fatalf("insertTestOperator(%s): %v", operatorID, err)
	}
}

// insertTestBooking writes an existing booking directly (bypassing the
// handler) to set up a conflict scenario. This still goes through the real
// no_overlapping_active_bookings trigger like any other insert - tests that
// need two existing rows on one asset must make sure those two don't
// themselves overlap.
func insertTestBooking(t *testing.T, bookingID, assetTag, siteID, startUTC, endUTC, status string, expiresAtUTC string) {
	t.Helper()
	var exp any
	if expiresAtUTC != "" {
		exp = expiresAtUTC
	}
	_, err := db.DB.Exec(
		`INSERT INTO bookings (booking_id, asset_tag, site_id, start_at, end_at, status, start_at_utc, end_at_utc, expires_at_utc)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bookingID, assetTag, siteID, startUTC, endUTC, status, startUTC, endUTC, exp,
	)
	if err != nil {
		t.Fatalf("insertTestBooking(%s): %v", bookingID, err)
	}
}

func tokenFor(role string, siteIDs []string) string {
	return token(map[string]any{"sub": "u-test-" + role, "role": role, "site_ids": siteIDs})
}

func postReservation(body map[string]any, bearer string) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/reservations", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
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

// rfc3339 formats a time as canonical UTC text. The .UTC() call is not
// cosmetic: time.Now() returns a time.Time in the host's local zone, and
// the trigger/pre-check overlap comparisons are plain string comparisons
// against SQLite's datetime('now') (always UTC) - a "-04:00"-offset RFC3339
// string compares incorrectly against that, silently breaking the
// expiry/overlap check (caught empirically while debugging this exact test
// file: a non-UTC expires_at_utc made an unexpired tentative hold fail to
// block). The production handler already converts with .UTC() before
// formatting for this reason; test fixtures must match that convention.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

var farFutureBase = time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)

func TestCreateReservation_HappyPath_ConfirmedNoCertRequired(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")

	code, body := postReservation(map[string]any{
		"asset_tag": asset, "site_id": site,
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(8 * time.Hour)),
		"status": "confirmed",
	}, tokenFor("ops_director", []string{"*"}))

	if code != 201 {
		t.Fatalf("expected 201, got %d: %+v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["booking_id"] == "" || data["booking_id"] == nil {
		t.Fatalf("expected a booking_id, got %+v", data)
	}
	if data["status"] != "confirmed" {
		t.Fatalf("expected status confirmed, got %+v", data)
	}
}

func TestCreateReservation_HappyPath_CertRequiredValidOperator(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	op := "ZO" + nextTestID()
	insertTestEquipment(t, asset, "active", "NCCCO Mobile Crane")
	insertTestSite(t, site, "America/New_York")
	insertTestOperator(t, op, "NCCCO Mobile Crane (exp 2030-01-01)")

	code, body := postReservation(map[string]any{
		"asset_tag": asset, "site_id": site, "operator_id": op,
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(8 * time.Hour)),
		"status": "confirmed",
	}, tokenFor("ops_director", []string{"*"}))

	if code != 201 {
		t.Fatalf("expected 201, got %d: %+v", code, body)
	}
}

func TestCreateReservation_HappyPath_Tentative(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")

	code, body := postReservation(map[string]any{
		"asset_tag": asset, "site_id": site,
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(8 * time.Hour)),
		"status": "tentative", "expires_at": rfc3339(time.Now().Add(time.Hour)),
	}, tokenFor("ops_director", []string{"*"}))

	if code != 201 {
		t.Fatalf("expected 201, got %d: %+v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["expires_at"] == nil || data["expires_at"] == "" {
		t.Fatalf("expected expires_at to be echoed back, got %+v", data)
	}
}

func TestCreateReservation_MissingRequiredFields(t *testing.T) {
	full := func() map[string]any {
		asset := "ZT" + nextTestID()
		site := "ZS" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestSite(t, site, "America/New_York")
		return map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(8 * time.Hour)),
			"status": "confirmed",
		}
	}

	for _, field := range []string{"asset_tag", "site_id", "start", "end", "status"} {
		t.Run("missing_"+field, func(t *testing.T) {
			body := full()
			delete(body, field)
			code, respBody := postReservation(body, tokenFor("ops_director", []string{"*"}))
			if code != 400 {
				t.Fatalf("expected 400 for missing %s, got %d: %+v", field, code, respBody)
			}
		})
	}
}

func TestCreateReservation_EndNotAfterStart(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")
	tok := tokenFor("ops_director", []string{"*"})

	cases := map[string]time.Time{
		"end_before_start": farFutureBase.Add(-time.Hour),
		"end_equals_start": farFutureBase,
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			code, body := postReservation(map[string]any{
				"asset_tag": asset, "site_id": site,
				"start": rfc3339(farFutureBase), "end": rfc3339(end), "status": "confirmed",
			}, tok)
			if code != 400 {
				t.Fatalf("expected 400, got %d: %+v", code, body)
			}
		})
	}
}

func TestCreateReservation_InvalidStatus(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")

	code, body := postReservation(map[string]any{
		"asset_tag": asset, "site_id": site,
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)),
		"status": "cancelled",
	}, tokenFor("ops_director", []string{"*"}))
	if code != 400 {
		t.Fatalf("expected 400, got %d: %+v", code, body)
	}
}

func TestCreateReservation_TentativeExpiresAtRules(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")
	tok := tokenFor("ops_director", []string{"*"})
	base := map[string]any{
		"asset_tag": asset, "site_id": site,
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(8 * time.Hour)),
	}

	t.Run("tentative_missing_expires_at", func(t *testing.T) {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		body["status"] = "tentative"
		code, resp := postReservation(body, tok)
		if code != 400 {
			t.Fatalf("expected 400, got %d: %+v", code, resp)
		}
	})

	t.Run("tentative_expires_at_in_past", func(t *testing.T) {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		body["status"] = "tentative"
		body["expires_at"] = rfc3339(time.Now().Add(-time.Hour))
		code, resp := postReservation(body, tok)
		if code != 400 {
			t.Fatalf("expected 400, got %d: %+v", code, resp)
		}
	})

	t.Run("confirmed_with_expires_at_set", func(t *testing.T) {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		body["status"] = "confirmed"
		body["expires_at"] = rfc3339(time.Now().Add(time.Hour))
		code, resp := postReservation(body, tok)
		if code != 400 {
			t.Fatalf("expected 400, got %d: %+v", code, resp)
		}
	})
}

func TestCreateReservation_UnparseableDates(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")

	code, body := postReservation(map[string]any{
		"asset_tag": asset, "site_id": site,
		"start": "not-a-date", "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
	}, tokenFor("ops_director", []string{"*"}))
	if code != 400 {
		t.Fatalf("expected 400, got %d: %+v", code, body)
	}
}

func TestCreateReservation_UnknownOrInactiveEquipment(t *testing.T) {
	site := "ZS" + nextTestID()
	insertTestSite(t, site, "America/New_York")
	tok := tokenFor("ops_director", []string{"*"})

	t.Run("unknown_asset_tag", func(t *testing.T) {
		code, body := postReservation(map[string]any{
			"asset_tag": "ZT-does-not-exist-" + nextTestID(), "site_id": site,
			"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
		}, tok)
		if code != 400 {
			t.Fatalf("expected 400, got %d: %+v", code, body)
		}
	})

	t.Run("retired_equipment", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "retired", "")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
		}, tok)
		if code != 400 {
			t.Fatalf("expected 400, got %d: %+v", code, body)
		}
	})
}

func TestCreateReservation_AssetTagNormalization(t *testing.T) {
	asset := "ZTNORM" + nextTestID()
	site := "ZS" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")

	dirty := strings.ToLower(asset[:2]) + "-" + asset[2:] + " "
	code, body := postReservation(map[string]any{
		"asset_tag": dirty, "site_id": site,
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
	}, tokenFor("ops_director", []string{"*"}))
	if code != 201 {
		t.Fatalf("expected 201 (asset_tag should normalize to match %q), got %d: %+v", asset, code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["asset_tag"] != asset {
		t.Fatalf("expected canonical asset_tag %q in response, got %+v", asset, data["asset_tag"])
	}
}

func TestCreateReservation_UnknownSite(t *testing.T) {
	asset := "ZT" + nextTestID()
	insertTestEquipment(t, asset, "active", "")

	code, body := postReservation(map[string]any{
		"asset_tag": asset, "site_id": "ZS-does-not-exist-" + nextTestID(),
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
	}, tokenFor("ops_director", []string{"*"}))
	if code != 400 {
		t.Fatalf("expected 400, got %d: %+v", code, body)
	}
}

func TestCreateReservation_CertRequiredOperatorChecks(t *testing.T) {
	site := "ZS" + nextTestID()
	insertTestSite(t, site, "America/New_York")
	tok := tokenFor("ops_director", []string{"*"})
	start := farFutureBase
	end := farFutureBase.Add(8 * time.Hour)

	t.Run("missing_operator_id", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "NCCCO Mobile Crane")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tok)
		if code != 400 {
			t.Fatalf("expected 400, got %d: %+v", code, body)
		}
	})

	t.Run("unknown_operator_id", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "NCCCO Mobile Crane")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site, "operator_id": "ZO-does-not-exist-" + nextTestID(),
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tok)
		if code != 400 {
			t.Fatalf("expected 400, got %d: %+v", code, body)
		}
	})

	t.Run("operator_lacks_matching_cert", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		op := "ZO" + nextTestID()
		insertTestEquipment(t, asset, "active", "NCCCO Mobile Crane")
		insertTestOperator(t, op, "OSHA 30 (exp 2030-01-01)")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site, "operator_id": op,
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tok)
		if code != 422 {
			t.Fatalf("expected 422, got %d: %+v", code, body)
		}
	})

	t.Run("operator_cert_expires_before_booking_end", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		op := "ZO" + nextTestID()
		insertTestEquipment(t, asset, "active", "NCCCO Mobile Crane")
		// end-of-day expiry the day BEFORE the booking's end date.
		insertTestOperator(t, op, fmt.Sprintf("NCCCO Mobile Crane (exp %s)", end.AddDate(0, 0, -1).Format("2006-01-02")))
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site, "operator_id": op,
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tok)
		if code != 422 {
			t.Fatalf("expected 422, got %d: %+v", code, body)
		}
	})

	t.Run("operator_cert_expires_exactly_at_booking_end_is_valid", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		op := "ZO" + nextTestID()
		insertTestEquipment(t, asset, "active", "NCCCO Mobile Crane")
		// end is 2027-03-01T16:00:00Z; end-of-day in America/New_York (EST,
		// UTC-5 in March before DST) on 2027-03-01 is 2027-03-02T05:00:00Z,
		// which is after end - this is the inclusive boundary case.
		insertTestOperator(t, op, "NCCCO Mobile Crane (exp 2027-03-01)")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site, "operator_id": op,
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tok)
		if code != 201 {
			t.Fatalf("expected 201, got %d: %+v", code, body)
		}
	})

	t.Run("non_cert_equipment_operator_omitted", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tok)
		if code != 201 {
			t.Fatalf("expected 201, got %d: %+v", code, body)
		}
	})

	t.Run("non_cert_equipment_unknown_operator_still_validated", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site, "operator_id": "ZO-does-not-exist-" + nextTestID(),
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tok)
		if code != 400 {
			t.Fatalf("expected 400, got %d: %+v", code, body)
		}
	})
}

func TestCreateReservation_Permissions(t *testing.T) {
	siteOwn := "ZS" + nextTestID()
	siteOther := "ZS" + nextTestID()
	insertTestSite(t, siteOwn, "America/New_York")
	insertTestSite(t, siteOther, "America/New_York")

	newBody := func(site string) map[string]any {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		return map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
		}
	}

	cases := []struct {
		name     string
		role     string
		siteIDs  []string
		site     string
		wantCode int
	}{
		{"ops_director_any_site", "ops_director", []string{"*"}, siteOther, 201},
		{"superintendent_own_site", "superintendent", []string{siteOwn}, siteOwn, 201},
		{"superintendent_other_site", "superintendent", []string{siteOwn}, siteOther, 403},
		{"foreman_own_site", "foreman", []string{siteOwn}, siteOwn, 201},
		{"foreman_other_site", "foreman", []string{siteOwn}, siteOther, 403},
		{"subcontractor_any_site", "subcontractor", []string{siteOwn}, siteOwn, 403},
		{"safety_manager_wildcard_site_ids_still_forbidden", "safety_manager", []string{"*"}, siteOwn, 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := postReservation(newBody(c.site), tokenFor(c.role, c.siteIDs))
			if code != c.wantCode {
				t.Fatalf("expected %d, got %d: %+v", c.wantCode, code, body)
			}
		})
	}
}

func TestCreateReservation_NoToken(t *testing.T) {
	code, _ := postReservation(map[string]any{
		"asset_tag": "ZT-whatever", "site_id": "ZS-whatever",
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
	}, "")
	if code != 401 {
		t.Fatalf("expected 401, got %d", code)
	}
}

func TestCreateReservation_OverlapGeometry(t *testing.T) {
	site := "ZS" + nextTestID()
	insertTestSite(t, site, "America/New_York")
	tok := tokenFor("ops_director", []string{"*"})

	existingStart := farFutureBase
	existingEnd := farFutureBase.Add(8 * time.Hour)

	setup := func() string {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestBooking(t, "ZB"+nextTestID(), asset, site, rfc3339(existingStart), rfc3339(existingEnd), "confirmed", "")
		return asset
	}

	overlapping := []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{"exact_duplicate", existingStart, existingEnd},
		{"new_start_inside_existing", existingStart.Add(2 * time.Hour), existingEnd.Add(2 * time.Hour)},
		{"new_end_inside_existing", existingStart.Add(-2 * time.Hour), existingStart.Add(2 * time.Hour)},
		{"new_contains_existing", existingStart.Add(-time.Hour), existingEnd.Add(time.Hour)},
		{"existing_contains_new", existingStart.Add(time.Hour), existingEnd.Add(-time.Hour)},
	}
	for _, c := range overlapping {
		t.Run(c.name, func(t *testing.T) {
			asset := setup()
			code, body := postReservation(map[string]any{
				"asset_tag": asset, "site_id": site,
				"start": rfc3339(c.start), "end": rfc3339(c.end), "status": "confirmed",
			}, tok)
			if code != 409 {
				t.Fatalf("expected 409, got %d: %+v", code, body)
			}
			// Asserting on the conflict detail, not just the 409 status
			// code, matters: found empirically during the deliberate-break
			// exercise that status-code-only assertions here can't tell
			// the app-level pre-check (which returns real detail) apart
			// from the schema trigger backstop alone (which returns a
			// bare {"error":"conflict"} with no "conflict" key) - either
			// one alone makes this specific assertion pass, but only the
			// pre-check satisfies this one.
			conflict, _ := body["conflict"].(map[string]any)
			if conflict == nil || conflict["booking_id"] == nil {
				t.Fatalf("expected conflict detail in the response body, got %+v", body)
			}
		})
	}

	t.Run("adjacent_non_overlapping", func(t *testing.T) {
		asset := setup()
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(existingEnd), "end": rfc3339(existingEnd.Add(time.Hour)), "status": "confirmed",
		}, tok)
		if code != 201 {
			t.Fatalf("expected 201 (touching boundary is not an overlap), got %d: %+v", code, body)
		}
	})

	t.Run("disjoint_same_asset", func(t *testing.T) {
		asset := setup()
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(existingEnd.Add(24 * time.Hour)), "end": rfc3339(existingEnd.Add(32 * time.Hour)), "status": "confirmed",
		}, tok)
		if code != 201 {
			t.Fatalf("expected 201, got %d: %+v", code, body)
		}
	})

	t.Run("overlapping_different_asset", func(t *testing.T) {
		setup() // asset A, booked
		assetB := "ZT" + nextTestID()
		insertTestEquipment(t, assetB, "active", "")
		code, body := postReservation(map[string]any{
			"asset_tag": assetB, "site_id": site,
			"start": rfc3339(existingStart), "end": rfc3339(existingEnd), "status": "confirmed",
		}, tok)
		if code != 201 {
			t.Fatalf("expected 201 (different asset, same window), got %d: %+v", code, body)
		}
	})

	t.Run("cancelled_existing_does_not_block", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestBooking(t, "ZB"+nextTestID(), asset, site, rfc3339(existingStart), rfc3339(existingEnd), "cancelled", "")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(existingStart), "end": rfc3339(existingEnd), "status": "confirmed",
		}, tok)
		if code != 201 {
			t.Fatalf("expected 201 (cancelled never blocks), got %d: %+v", code, body)
		}
	})

	t.Run("unexpired_tentative_blocks", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestBooking(t, "ZB"+nextTestID(), asset, site, rfc3339(existingStart), rfc3339(existingEnd), "tentative", rfc3339(time.Now().Add(time.Hour)))
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(existingStart), "end": rfc3339(existingEnd), "status": "confirmed",
		}, tok)
		if code != 409 {
			t.Fatalf("expected 409 (unexpired tentative hold blocks), got %d: %+v", code, body)
		}
		conflict, _ := body["conflict"].(map[string]any)
		if conflict == nil || conflict["booking_id"] == nil {
			t.Fatalf("expected conflict detail in the response body, got %+v", body)
		}
	})

	t.Run("expired_tentative_does_not_block", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestBooking(t, "ZB"+nextTestID(), asset, site, rfc3339(existingStart), rfc3339(existingEnd), "tentative", rfc3339(time.Now().Add(-time.Hour)))
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": site,
			"start": rfc3339(existingStart), "end": rfc3339(existingEnd), "status": "confirmed",
		}, tok)
		if code != 201 {
			t.Fatalf("expected 201 (expired tentative hold doesn't block), got %d: %+v", code, body)
		}
	})
}

func TestCreateReservation_ConflictRedaction(t *testing.T) {
	siteOwn := "ZS" + nextTestID()
	siteOther := "ZS" + nextTestID()
	insertTestSite(t, siteOwn, "America/New_York")
	insertTestSite(t, siteOther, "America/New_York")
	start := farFutureBase
	end := farFutureBase.Add(8 * time.Hour)

	t.Run("ops_director_sees_full_detail", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestBooking(t, "ZB"+nextTestID(), asset, siteOwn, rfc3339(start), rfc3339(end), "confirmed", "")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": siteOwn,
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tokenFor("ops_director", []string{"*"}))
		if code != 409 {
			t.Fatalf("expected 409, got %d: %+v", code, body)
		}
		conflict, _ := body["conflict"].(map[string]any)
		if conflict["booking_id"] == nil {
			t.Fatalf("ops_director should see full conflict detail, got %+v", conflict)
		}
	})

	t.Run("foreman_sees_full_detail_for_conflict_at_own_site", func(t *testing.T) {
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestBooking(t, "ZB"+nextTestID(), asset, siteOwn, rfc3339(start), rfc3339(end), "confirmed", "")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": siteOwn,
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tokenFor("foreman", []string{siteOwn}))
		if code != 409 {
			t.Fatalf("expected 409, got %d: %+v", code, body)
		}
		conflict, _ := body["conflict"].(map[string]any)
		if conflict["booking_id"] == nil {
			t.Fatalf("foreman should see full detail for a conflict at their own site, got %+v", conflict)
		}
	})

	t.Run("foreman_sees_redacted_detail_when_conflicting_row_is_at_another_site", func(t *testing.T) {
		// Same asset booked at a DIFFERENT site than the one in this
		// request - the caller is authorized to book at siteOwn, but the
		// existing conflicting row is at siteOther, which isn't in their
		// site_ids. The asset_tag match (not site_id) is what makes this
		// a conflict at all; the redaction rule still applies per-row.
		asset := "ZT" + nextTestID()
		insertTestEquipment(t, asset, "active", "")
		insertTestBooking(t, "ZB"+nextTestID(), asset, siteOther, rfc3339(start), rfc3339(end), "confirmed", "")
		code, body := postReservation(map[string]any{
			"asset_tag": asset, "site_id": siteOwn,
			"start": rfc3339(start), "end": rfc3339(end), "status": "confirmed",
		}, tokenFor("foreman", []string{siteOwn}))
		if code != 409 {
			t.Fatalf("expected 409, got %d: %+v", code, body)
		}
		conflict, _ := body["conflict"].(map[string]any)
		if conflict["booking_id"] != nil {
			t.Fatalf("foreman should NOT see detail for a conflict at a site outside their site_ids, got %+v", conflict)
		}
		if conflict["exists"] != true {
			t.Fatalf("expected redacted {exists:true}, got %+v", conflict)
		}
	})
}

func TestCreateReservation_AuditFieldsNotSpoofable(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	insertTestEquipment(t, asset, "active", "")
	insertTestSite(t, site, "America/New_York")

	code, body := postReservation(map[string]any{
		"asset_tag": asset, "site_id": site,
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
		"booked_by": "someone-else-entirely",
	}, tokenFor("ops_director", []string{"*"}))
	if code != 201 {
		t.Fatalf("expected 201, got %d: %+v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["booked_by"] != "u-test-ops_director" {
		t.Fatalf("expected booked_by to come from the verified JWT sub, got %+v", data["booked_by"])
	}
}

func TestCreateReservation_ResponseHasNoOperatorPII(t *testing.T) {
	asset := "ZT" + nextTestID()
	site := "ZS" + nextTestID()
	op := "ZO" + nextTestID()
	insertTestEquipment(t, asset, "active", "NCCCO Mobile Crane")
	insertTestSite(t, site, "America/New_York")
	insertTestOperator(t, op, "NCCCO Mobile Crane (exp 2030-01-01)")

	code, body := postReservation(map[string]any{
		"asset_tag": asset, "site_id": site, "operator_id": op,
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
	}, tokenFor("ops_director", []string{"*"}))
	if code != 201 {
		t.Fatalf("expected 201, got %d: %+v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	for _, piiKey := range []string{"name", "first_name", "last_name", "phone", "email", "ssn_last4", "date_of_birth", "emergency_contact", "certifications"} {
		if _, present := data[piiKey]; present {
			t.Fatalf("response must not include operator PII key %q, got %+v", piiKey, data)
		}
	}
}

func TestCreateReservation_SQLMetacharacterInputIsPlainlyRejected(t *testing.T) {
	code, body := postReservation(map[string]any{
		"asset_tag": "x'); DROP TABLE bookings;--", "site_id": "ZS-whatever",
		"start": rfc3339(farFutureBase), "end": rfc3339(farFutureBase.Add(time.Hour)), "status": "confirmed",
	}, tokenFor("ops_director", []string{"*"}))
	if code != 400 {
		t.Fatalf("expected 400 (unknown asset_tag, not a server error), got %d: %+v", code, body)
	}

	// Confirm the bookings table is still intact - a real injection would
	// have dropped it.
	var count int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM bookings").Scan(&count); err != nil {
		t.Fatalf("bookings table appears to be gone: %v", err)
	}
}

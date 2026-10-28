package handlers_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"lumen/equipment-api/internal/config"
	"lumen/equipment-api/internal/db"
	"lumen/equipment-api/internal/fleetcare"
	"lumen/equipment-api/internal/handlers"
	"lumen/equipment-api/internal/server"
)

// NOTE: requires `npm run seed` from the repo root first.

// alwaysOKFetcher is the default FleetCare double for tests that don't care
// about maintenance behavior: every asset comes back with zero windows, so
// `available` reflects booking state alone, same as before FleetCare
// integration existed. Tests that DO care about maintenance call
// handlers.InitFleetCareWithFetcher themselves and restore this default via
// t.Cleanup so test order never matters.
type alwaysOKFetcher struct{}

func (alwaysOKFetcher) FetchAll(ctx context.Context, tags []string) (map[string][]fleetcare.Window, error) {
	out := map[string][]fleetcare.Window{}
	for _, tag := range tags {
		out[tag] = []fleetcare.Window{}
	}
	return out, nil
}

func resetFleetCareDefault() {
	handlers.InitFleetCareWithFetcher(alwaysOKFetcher{}, time.Minute, time.Hour)
}

func TestMain(m *testing.M) {
	os.Setenv("REPO_ROOT", "../../..")
	// Set before LoadEnv (which only fills in unset vars) so tests don't
	// depend on the ambient .env's secret meeting RequireAuth's minimum
	// length - exactly the brittleness write-tests warns about. This is
	// not a workaround for a bug: the real local .env's JWT_SECRET is
	// shorter than 32 bytes, and RequireAuth now correctly refuses to
	// start with it (see docs/reviews/2026-10-28-part1-auth-review.md).
	os.Setenv("JWT_SECRET", "test-secret-for-handlers-tests-32-bytes-min")
	config.LoadEnv()
	db.Open()
	resetFleetCareDefault()
	os.Exit(m.Run())
}

// token signs claims exactly as scripts/dev-token.mjs does, including
// real exp/iss/aud claims - RequireAuth now verifies the signature and
// requires a valid, unexpired exp plus a matching issuer/audience, so a
// test fixture without them would be rejected, correctly, but wouldn't be
// testing what these tests are meant to test.
func token(claims map[string]any) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = "lumen-dev"
	}
	if _, ok := claims["aud"]; !ok {
		claims["aud"] = "lumen-equipment-api"
	}
	p, _ := json.Marshal(claims)
	unsigned := enc.EncodeToString(h) + "." + enc.EncodeToString(p)
	mac := hmac.New(sha256.New, []byte(os.Getenv("JWT_SECRET")))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func TestAvailabilityRequiresToken(t *testing.T) {
	rec := httptest.NewRecorder()
	server.New().ServeHTTP(rec, httptest.NewRequest("GET", "/api/equipment/availability", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAvailabilityReturnsCranes(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/equipment/availability?type=mobile_crane&start=2026-10-13&end=2026-10-15", nil)
	req.Header.Set("Authorization", "Bearer "+token(map[string]any{"sub": "u-ops-01", "role": "ops_director", "site_ids": []string{"*"}}))
	rec := httptest.NewRecorder()
	server.New().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Data) == 0 {
		t.Fatal("expected some cranes")
	}
	for _, e := range body.Data {
		if e["type"] != "mobile_crane" {
			t.Fatalf("unexpected type %v", e["type"])
		}
	}
}

// --- Increment 1 regression tests: overlap/date-window correctness ---
// All use real seeded booking IDs from data/bookings.csv, confirmed against
// this exact geometry in the live runtime-evidence pass earlier in this
// engagement. Query windows are expressed in RFC3339/UTC to avoid ambiguity
// about which timezone a bare query param means (that's formalized in the
// API-validation increment); only the booking's own site timezone matters
// for interpreting its stored naive timestamp.

func queryAvailability(t *testing.T, qs string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/equipment/availability?"+qs, nil)
	req.Header.Set("Authorization", "Bearer "+token(map[string]any{"sub": "u-ops-01", "role": "ops_director", "site_ids": []string{"*"}}))
	rec := httptest.NewRecorder()
	server.New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return body.Data
}

func findItem(t *testing.T, data []map[string]any, assetTag string) map[string]any {
	t.Helper()
	for _, item := range data {
		if item["asset_tag"] == assetTag {
			return item
		}
	}
	t.Fatalf("asset_tag %q not found in response (%d items)", assetTag, len(data))
	return nil
}

// Scenario 1 / 8: booking contained in query window (MC-007/BK-1119,
// 2026-11-12 07:30-17:00 EST, confirmed) must conflict, tagged confirmed.
func TestAvailability_BookingContainedInQuery(t *testing.T) {
	data := queryAvailability(t, "type=mobile_crane&start=2026-11-10T00:00:00Z&end=2026-11-14T00:00:00Z")
	mc007 := findItem(t, data, "MC-007")
	if mc007["available"] != false {
		t.Fatalf("MC-007 should be unavailable (BK-1119), got available=%v", mc007["available"])
	}
	conflict, _ := mc007["conflict"].(map[string]any)
	if conflict == nil || conflict["booking_id"] != "BK-1119" || conflict["status"] != "confirmed" {
		t.Fatalf("expected conflict BK-1119/confirmed, got %+v", conflict)
	}
}

// Scenario 2: query contained in booking (MC-007/BK-1005, Nov15-25
// confirmed) - this is the exact inherited bug. Must now conflict.
func TestAvailability_QueryContainedInBooking(t *testing.T) {
	data := queryAvailability(t, "type=mobile_crane&start=2026-11-20T00:00:00Z&end=2026-11-22T00:00:00Z")
	mc007 := findItem(t, data, "MC-007")
	if mc007["available"] != false {
		t.Fatalf("MC-007 should be unavailable (BK-1005 spans the whole query window), got available=%v", mc007["available"])
	}
	conflict, _ := mc007["conflict"].(map[string]any)
	if conflict == nil || conflict["booking_id"] != "BK-1005" {
		t.Fatalf("expected conflict BK-1005, got %+v", conflict)
	}
}

// Scenarios 3/4: partial overlap at either boundary must conflict.
func TestAvailability_PartialOverlapBothSides(t *testing.T) {
	left := queryAvailability(t, "type=mobile_crane&start=2026-11-12T00:00:00Z&end=2026-11-12T13:00:00Z")
	if findItem(t, left, "MC-007")["available"] != false {
		t.Fatal("partial overlap (query ends mid-booking) should conflict")
	}
	right := queryAvailability(t, "type=mobile_crane&start=2026-11-12T15:00:00Z&end=2026-11-12T23:59:00Z")
	if findItem(t, right, "MC-007")["available"] != false {
		t.Fatal("partial overlap (query starts mid-booking) should conflict")
	}
}

// Scenarios 5/6: windows that merely touch a booking boundary must NOT conflict.
func TestAvailability_AdjacentWindowsDoNotConflict(t *testing.T) {
	before := queryAvailability(t, "type=mobile_crane&start=2026-11-12T00:00:00Z&end=2026-11-12T12:30:00Z")
	if findItem(t, before, "MC-007")["available"] != true {
		t.Fatal("a window ending exactly at booking start must not conflict")
	}
	after := queryAvailability(t, "type=mobile_crane&start=2026-11-12T22:00:00Z&end=2026-11-13T00:00:00Z")
	if findItem(t, after, "MC-007")["available"] != true {
		t.Fatal("a window starting exactly at booking end must not conflict")
	}
}

// Scenario 7: a cancelled booking (MC-007/BK-1368, Nov1-6 2026) must never conflict.
func TestAvailability_CancelledBookingNeverBlocks(t *testing.T) {
	data := queryAvailability(t, "type=mobile_crane&start=2026-11-01T00:00:00Z&end=2026-11-07T00:00:00Z")
	mc007 := findItem(t, data, "MC-007")
	if mc007["available"] != true {
		t.Fatalf("MC-007 should be available (BK-1368 is cancelled), got %+v", mc007["conflict"])
	}
}

// Scenario 9: a tentative booking (MC-007/BK-1117, Jan1-3 2027) still blocks
// today (A1's approved default), tagged tentative so a consumer can tell.
func TestAvailability_TentativeBookingBlocksAndIsTagged(t *testing.T) {
	data := queryAvailability(t, "type=mobile_crane&start=2027-01-01T00:00:00Z&end=2027-01-04T00:00:00Z")
	mc007 := findItem(t, data, "MC-007")
	if mc007["available"] != false {
		t.Fatal("MC-007 should be unavailable (BK-1117, tentative)")
	}
	conflict, _ := mc007["conflict"].(map[string]any)
	if conflict == nil || conflict["booking_id"] != "BK-1117" || conflict["status"] != "tentative" {
		t.Fatalf("expected conflict BK-1117/tentative, got %+v", conflict)
	}
}

// Scenario 15: a lowercase, mismatched asset_tag (mc-005/BK-1572) must still
// correctly join and conflict, once normalized.
func TestAvailability_LowercaseTagStillJoins(t *testing.T) {
	data := queryAvailability(t, "type=mobile_crane&start=2026-12-16T00:00:00Z&end=2026-12-18T00:00:00Z")
	mc005 := findItem(t, data, "MC-005")
	if mc005["available"] != false {
		t.Fatal("MC-005 should be unavailable (BK-1572, stored as 'mc-005')")
	}
	conflict, _ := mc005["conflict"].(map[string]any)
	if conflict == nil || conflict["booking_id"] != "BK-1572" {
		t.Fatalf("expected conflict BK-1572, got %+v", conflict)
	}
}

// Scenario 22: retired equipment must never appear, regardless of the rewrite.
func TestAvailability_RetiredEquipmentExcluded(t *testing.T) {
	data := queryAvailability(t, "start=2026-10-01T00:00:00Z&end=2026-10-02T00:00:00Z")
	for _, item := range data {
		if item["asset_tag"] == "MC-008" || item["asset_tag"] == "SL-005" {
			t.Fatalf("retired equipment %v must not appear in results", item["asset_tag"])
		}
	}
}

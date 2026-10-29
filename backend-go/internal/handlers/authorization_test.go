package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lumen/equipment-api/internal/server"
)

func queryAvailabilityAs(t *testing.T, qs string, claims map[string]any) []map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/equipment/availability?"+qs, nil)
	req.Header.Set("Authorization", "Bearer "+token(claims))
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

// MC-007/BK-1119 is at S02, confirmed, 2026-11-12 07:30-17:00 (same fixture
// increment 1 used). u-sup-01-equivalent claims cover S01/S02/S05.
const mc007Window = "type=mobile_crane&start=2026-11-10T00:00:00Z&end=2026-11-14T00:00:00Z"

func claimsFor(sub, role string, siteIDs []string) map[string]any {
	return map[string]any{"sub": sub, "role": role, "site_ids": siteIDs}
}

// Scenario 10: ops_director gets full conflict detail for any site.
func TestAuthorization_OpsDirectorSeesFullDetail(t *testing.T) {
	data := queryAvailabilityAs(t, mc007Window, claimsFor("u-ops-01", "ops_director", []string{"*"}))
	mc007 := findItem(t, data, "MC-007")
	conflict, _ := mc007["conflict"].(map[string]any)
	if conflict == nil || conflict["booking_id"] != "BK-1119" || conflict["site_id"] != "S02" {
		t.Fatalf("ops_director should see full detail, got %+v", conflict)
	}
}

// Scenario 11: superintendent/foreman at the conflict's own site get full detail.
func TestAuthorization_OwnSiteSeesFullDetail(t *testing.T) {
	for _, role := range []string{"superintendent", "foreman"} {
		data := queryAvailabilityAs(t, mc007Window, claimsFor("u-test", role, []string{"S01", "S02", "S05"}))
		mc007 := findItem(t, data, "MC-007")
		conflict, _ := mc007["conflict"].(map[string]any)
		if conflict == nil || conflict["booking_id"] != "BK-1119" {
			t.Fatalf("%s at the conflict's own site should see full detail, got %+v", role, conflict)
		}
	}
}

// Scenario 12: superintendent/foreman at a DIFFERENT site get redacted detail.
func TestAuthorization_OtherSiteGetsRedacted(t *testing.T) {
	for _, role := range []string{"superintendent", "foreman"} {
		data := queryAvailabilityAs(t, mc007Window, claimsFor("u-test", role, []string{"S09", "S10"}))
		mc007 := findItem(t, data, "MC-007")
		if mc007["available"] != false {
			t.Fatalf("%s should still see the asset as unavailable", role)
		}
		conflict, _ := mc007["conflict"].(map[string]any)
		if conflict == nil {
			t.Fatalf("%s should still see that a conflict exists", role)
		}
		if _, hasBookingID := conflict["booking_id"]; hasBookingID {
			t.Fatalf("%s at a different site must not see booking_id, got %+v", role, conflict)
		}
		if _, hasSiteID := conflict["site_id"]; hasSiteID {
			t.Fatalf("%s at a different site must not see site_id, got %+v", role, conflict)
		}
		if conflict["exists"] != true {
			t.Fatalf("%s should see conflict:{exists:true}, got %+v", role, conflict)
		}
	}
}

// Scenario 13 (the review's carve-out): safety_manager's site_ids=["*"]
// must NOT grant it the ops_director-style bypass.
func TestAuthorization_SafetyManagerNoBypass(t *testing.T) {
	data := queryAvailabilityAs(t, mc007Window, claimsFor("u-saf-01", "safety_manager", []string{"*"}))
	mc007 := findItem(t, data, "MC-007")
	conflict, _ := mc007["conflict"].(map[string]any)
	if conflict == nil {
		t.Fatal("safety_manager should still see that a conflict exists")
	}
	if _, hasBookingID := conflict["booking_id"]; hasBookingID {
		t.Fatalf("safety_manager must not get full detail via its wildcard site_ids, got %+v", conflict)
	}
}

// Scenario 14: subcontractor never gets full detail, even at their own site.
func TestAuthorization_SubcontractorNeverGetsFullDetail(t *testing.T) {
	// BK-1244: MC-007 at S01, 2026-12-17 07:30 - 2026-12-22 17:00, confirmed.
	data := queryAvailabilityAs(t,
		"type=mobile_crane&start=2026-12-17T00:00:00Z&end=2026-12-23T00:00:00Z",
		claimsFor("u-sub-01", "subcontractor", []string{"S01"}))
	mc007 := findItem(t, data, "MC-007")
	conflict, _ := mc007["conflict"].(map[string]any)
	if conflict == nil || conflict["exists"] != true {
		t.Fatalf("subcontractor should see conflict exists, got %+v", conflict)
	}
	if _, hasBookingID := conflict["booking_id"]; hasBookingID {
		t.Fatalf("subcontractor must never see full detail, even at their own site (S01), got %+v", conflict)
	}
}

// Scenario 15: a forged/unrecognized role must fall to the most-restrictive branch.
func TestAuthorization_UnrecognizedRoleIsRedacted(t *testing.T) {
	data := queryAvailabilityAs(t, mc007Window, claimsFor("u-forged", "superuser", []string{"*"}))
	mc007 := findItem(t, data, "MC-007")
	conflict, _ := mc007["conflict"].(map[string]any)
	if conflict == nil || conflict["exists"] != true {
		t.Fatalf("an unrecognized role must be redacted, got %+v", conflict)
	}
	if _, hasBookingID := conflict["booking_id"]; hasBookingID {
		t.Fatalf("an unrecognized role must not see full detail, got %+v", conflict)
	}
}

// Scenario 16: daily_rate_usd only for ops_director/superintendent.
func TestAuthorization_DailyRateGatedByRole(t *testing.T) {
	cases := []struct {
		role      string
		siteIDs   []string
		visible   bool
	}{
		{"ops_director", []string{"*"}, true},
		{"superintendent", []string{"S01"}, true},
		{"foreman", []string{"S01"}, false},
		{"safety_manager", []string{"*"}, false},
		{"subcontractor", []string{"S01"}, false},
	}
	for _, c := range cases {
		data := queryAvailabilityAs(t, "type=mobile_crane&start=2026-10-01&end=2026-10-02", claimsFor("u-test", c.role, c.siteIDs))
		mc001 := findItem(t, data, "MC-001")
		_, present := mc001["daily_rate_usd"]
		if present != c.visible {
			t.Fatalf("%s: daily_rate_usd present=%v, want %v", c.role, present, c.visible)
		}
	}
}

// Scenario 17: operator PII must never appear, for any role, ever (A6 regression).
func TestAuthorization_OperatorPIINeverPresent(t *testing.T) {
	piiFields := []string{"first_name", "last_name", "phone", "email", "date_of_birth", "ssn_last4", "emergency_contact", "union_local", "operator_id", "certifications", "home_region"}
	for _, role := range []string{"ops_director", "superintendent", "foreman", "safety_manager", "subcontractor"} {
		data := queryAvailabilityAs(t, mc007Window, claimsFor("u-test", role, []string{"*", "S01", "S02", "S05"}))
		mc007 := findItem(t, data, "MC-007")
		conflict, _ := mc007["conflict"].(map[string]any)
		if conflict == nil {
			continue
		}
		if _, hasOperator := conflict["operator"]; hasOperator {
			t.Fatalf("%s: conflict.operator must not exist at all, got %+v", role, conflict["operator"])
		}
		for _, field := range piiFields {
			if _, present := conflict[field]; present {
				t.Fatalf("%s: PII field %q leaked directly into conflict: %+v", role, field, conflict)
			}
		}
	}
}

// Scenario 18: notes stays visible to everyone - a deliberate, documented
// exception (it's the only current home for safety hazard flags).
func TestAuthorization_NotesStaysVisible(t *testing.T) {
	data := queryAvailabilityAs(t, "type=mobile_crane&start=2026-10-01&end=2026-10-02", claimsFor("u-test", "subcontractor", []string{"S01"}))
	mc005 := findItem(t, data, "MC-005")
	if _, present := mc005["notes"]; !present {
		t.Fatal("notes must remain visible to every role (safety hazard flags live there)")
	}
}

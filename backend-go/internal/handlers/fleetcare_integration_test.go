package handlers_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"lumen/equipment-api/internal/fleetcare"
	"lumen/equipment-api/internal/handlers"
)

type fixedFetcher struct {
	windows map[string][]fleetcare.Window
	err     error
}

func (f fixedFetcher) FetchAll(ctx context.Context, tags []string) (map[string][]fleetcare.Window, error) {
	out := map[string][]fleetcare.Window{}
	for _, tag := range tags {
		if w, ok := f.windows[tag]; ok {
			out[tag] = w
		} else if f.err == nil {
			out[tag] = []fleetcare.Window{}
		}
	}
	return out, f.err
}

// Scenario 1: a maintenance window intersecting the query range forces
// unavailable, even with zero booking conflicts.
func TestAvailability_MaintenanceWindowForcesUnavailable(t *testing.T) {
	handlers.InitFleetCareWithFetcher(fixedFetcher{
		windows: map[string][]fleetcare.Window{
			"MC-004": {{
				StartsAt: time.Date(2026, 10, 2, 16, 0, 0, 0, time.UTC),
				EndsAt:   time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC),
				Kind:     "annual_inspection",
			}},
		},
	}, time.Minute, time.Hour)
	t.Cleanup(resetFleetCareDefault)

	data := queryAvailability(t, "type=mobile_crane&start=2026-10-02T00:00:00Z&end=2026-10-02T23:59:00Z")
	mc004 := findItem(t, data, "MC-004")
	if mc004["available"] != false {
		t.Fatalf("MC-004 should be unavailable due to its FleetCare maintenance window, got %+v", mc004)
	}
	if mc004["conflict"] != nil {
		t.Fatalf("MC-004 has no booking conflict in this window - the conflict field should be nil, got %+v", mc004["conflict"])
	}
	maint, _ := mc004["maintenance"].(map[string]any)
	if maint == nil || maint["status"] != "in_maintenance" {
		t.Fatalf("expected maintenance.status=in_maintenance, got %+v", maint)
	}
}

// Scenario 16 (the blocking review finding, closed): when FleetCare data is
// unknown, `available` itself must be false - not just a side-channel field
// a consumer might forget to check.
func TestAvailability_UnknownMaintenanceForcesUnavailable(t *testing.T) {
	handlers.InitFleetCareWithFetcher(fixedFetcher{
		err: errFleetCareDown,
	}, time.Minute, time.Hour)
	t.Cleanup(resetFleetCareDefault)

	data := queryAvailability(t, "type=mobile_crane&start=2026-10-02T00:00:00Z&end=2026-10-02T23:59:00Z")
	mc004 := findItem(t, data, "MC-004")
	if mc004["available"] != false {
		t.Fatalf("MC-004's available must be false when maintenance status is unknown (no booking conflict either), got %+v", mc004)
	}
	maint, _ := mc004["maintenance"].(map[string]any)
	if maint == nil || maint["status"] != "unknown" {
		t.Fatalf("expected maintenance.status=unknown, got %+v", maint)
	}
}

// Scenario 9: cold start (FleetCare never initialized at all) must also
// read unknown/unavailable, never ok-by-default.
func TestAvailability_NoFleetCareConfiguredReadsUnknown(t *testing.T) {
	// Simulate "never initialized" by pointing at a fetcher that always
	// errors and has never succeeded - equivalent cold-start behavior.
	handlers.InitFleetCareWithFetcher(fixedFetcher{err: errFleetCareDown}, time.Minute, time.Hour)
	t.Cleanup(resetFleetCareDefault)

	data := queryAvailability(t, "type=mobile_crane&start=2026-10-02T00:00:00Z&end=2026-10-02T23:59:00Z")
	for _, item := range data {
		maint, _ := item["maintenance"].(map[string]any)
		if maint == nil || maint["status"] != "unknown" {
			t.Fatalf("%v: expected unknown at cold start, got %+v", item["asset_tag"], maint)
		}
		if item["available"] != false {
			t.Fatalf("%v: expected unavailable while maintenance status is unknown, got %+v", item["asset_tag"], item["available"])
		}
	}
}

var errFleetCareDown = &fleetcareDownError{}

type fleetcareDownError struct{}

func (*fleetcareDownError) Error() string { return "fleetcare: simulated outage" }

// Scenario 15: end-to-end against the REAL FleetCare mock (not a double),
// run with CHAOS=off for a deterministic result. Skips gracefully if the
// mock isn't running, since this suite shouldn't require an external
// process just to compile/pass.
func TestAvailability_RealMockIntegration(t *testing.T) {
	mockURL := os.Getenv("FLEETCARE_TEST_URL")
	if mockURL == "" {
		mockURL = "http://localhost:4010"
	}
	healthClient := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := healthClient.Get(mockURL + "/v1/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Skipf("FleetCare mock not reachable at %s (run `CHAOS=off npm run mock` to exercise this test): %v", mockURL, err)
	}
	resp.Body.Close()

	handlers.InitFleetCare(mockURL, "fc_test_key", 3*time.Second, time.Minute, time.Hour)
	t.Cleanup(resetFleetCareDefault)

	// MC-001 has a real, documented FleetCare window 2026-11-08 to 2026-11-11
	// (scheduled_service) in services/maintenance-api/windows.json.
	data := queryAvailability(t, "type=mobile_crane&start=2026-11-09T00:00:00Z&end=2026-11-09T12:00:00Z")
	mc001 := findItem(t, data, "MC-001")
	maint, _ := mc001["maintenance"].(map[string]any)
	if maint == nil || maint["status"] != "in_maintenance" {
		t.Fatalf("expected MC-001 to be in_maintenance per the real mock's windows.json, got %+v", maint)
	}
	if mc001["available"] != false {
		t.Fatalf("expected MC-001 unavailable, got %+v", mc001["available"])
	}
}

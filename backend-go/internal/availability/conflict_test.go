package availability

import (
	"testing"
	"time"
)

func utc(y, m, d, h, mi int) time.Time {
	return time.Date(y, time.Month(m), d, h, mi, 0, 0, time.UTC)
}

var utcZone = func(string) *time.Location { return time.UTC }

// Scenario 7: a cancelled booking must never produce a conflict.
func TestFindConflict_CancelledNeverBlocks(t *testing.T) {
	bookings := []Booking{
		{BookingID: "BK-X", SiteID: "S11", StartAt: "2026-11-01 07:00", EndAt: "2026-11-06 15:00", Status: "cancelled"},
	}
	conflict, warnings := FindConflict(bookings, utcZone, utc(2026, 11, 2, 0, 0), utc(2026, 11, 4, 0, 0))
	if conflict != nil {
		t.Fatalf("cancelled booking must not block, got conflict %+v", conflict)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
}

// Scenarios 8-9: tentative and confirmed bookings both block (A1 default),
// and the response must carry which status caused it.
func TestFindConflict_TentativeAndConfirmedBlock(t *testing.T) {
	for _, status := range []string{"tentative", "confirmed"} {
		bookings := []Booking{
			{BookingID: "BK-X", SiteID: "S11", StartAt: "2026-11-01 07:00", EndAt: "2026-11-06 15:00", Status: status},
		}
		conflict, _ := FindConflict(bookings, utcZone, utc(2026, 11, 2, 0, 0), utc(2026, 11, 4, 0, 0))
		if conflict == nil {
			t.Fatalf("%s booking must block", status)
		}
		if conflict.Status != status {
			t.Fatalf("conflict.Status = %q, want %q", conflict.Status, status)
		}
	}
}

// Scenario 19: an unparseable date on one row must produce a warning and be
// skipped, not crash the whole request and not silently vanish.
func TestFindConflict_UnparseableDateIsWarnedAndSkipped(t *testing.T) {
	bookings := []Booking{
		{BookingID: "BK-BAD", SiteID: "S11", StartAt: "not-a-date", EndAt: "2026-11-06 15:00", Status: "confirmed"},
	}
	conflict, warnings := FindConflict(bookings, utcZone, utc(2026, 11, 2, 0, 0), utc(2026, 11, 4, 0, 0))
	if conflict != nil {
		t.Fatalf("an unparseable row must not be treated as a conflict, got %+v", conflict)
	}
	if len(warnings) != 1 || warnings[0].BookingID != "BK-BAD" {
		t.Fatalf("expected exactly one warning for BK-BAD, got %+v", warnings)
	}
}

// Scenario 20: end <= start must never block and must not panic.
func TestFindConflict_EndBeforeStartNeverBlocks(t *testing.T) {
	bookings := []Booking{
		{BookingID: "BK-BAD2", SiteID: "S11", StartAt: "2026-11-06 15:00", EndAt: "2026-11-01 07:00", Status: "confirmed"},
	}
	conflict, _ := FindConflict(bookings, utcZone, utc(2026, 11, 2, 0, 0), utc(2026, 11, 4, 0, 0))
	if conflict != nil {
		t.Fatalf("end-before-start booking must never block, got %+v", conflict)
	}
}

// Scenario 18: a booking whose asset_tag doesn't normalize to any known
// equipment tag must be flagged, not silently dropped.
func TestBuildEquipmentIndex_OrphanedTagIsWarned(t *testing.T) {
	bookings := []Booking{
		{BookingID: "BK-ORPHAN", AssetTag: "ZZ-999", Status: "confirmed"},
		{BookingID: "BK-OK", AssetTag: "mc-005", Status: "confirmed"},
	}
	index, warnings := BuildEquipmentIndex(bookings, []string{"MC-005"})
	if len(warnings) != 1 || warnings[0].BookingID != "BK-ORPHAN" {
		t.Fatalf("expected exactly one warning for BK-ORPHAN, got %+v", warnings)
	}
	if len(index["MC005"]) != 1 || index["MC005"][0].BookingID != "BK-OK" {
		t.Fatalf("expected BK-OK indexed under MC005, got %+v", index)
	}
}

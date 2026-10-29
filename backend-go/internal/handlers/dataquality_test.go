package handlers_test

import (
	"testing"
	"time"

	"lumen/equipment-api/internal/availability"
	"lumen/equipment-api/internal/db"
)

// Scenario 21: run the real normalization/parsing logic against the full
// reseeded historical dataset (743 bookings from data/bookings.csv as of
// this writing), not a synthetic fixture. If the real data violates an
// assumption the new logic makes, this test reports exactly which rows and
// why, rather than hiding it or silently weakening the rule to pass.
func TestAvailability_FullRealDatasetNormalizes(t *testing.T) {
	eqRows, err := db.DB.Query("SELECT asset_tag FROM equipment")
	if err != nil {
		t.Fatalf("loading equipment: %v", err)
	}
	var equipmentTags []string
	for eqRows.Next() {
		var tag string
		if err := eqRows.Scan(&tag); err != nil {
			t.Fatalf("scanning equipment: %v", err)
		}
		equipmentTags = append(equipmentTags, tag)
	}
	eqRows.Close()
	if len(equipmentTags) == 0 {
		t.Fatal("no equipment loaded - is the DB seeded? run `npm run seed` first")
	}

	bkRows, err := db.DB.Query("SELECT booking_id, asset_tag, site_id, start_at, end_at, status FROM bookings")
	if err != nil {
		t.Fatalf("loading bookings: %v", err)
	}
	var bookings []availability.Booking
	for bkRows.Next() {
		var b availability.Booking
		if err := bkRows.Scan(&b.BookingID, &b.AssetTag, &b.SiteID, &b.StartAt, &b.EndAt, &b.Status); err != nil {
			t.Fatalf("scanning booking: %v", err)
		}
		bookings = append(bookings, b)
	}
	bkRows.Close()
	if len(bookings) == 0 {
		t.Fatal("no bookings loaded - is the DB seeded? run `npm run seed` first")
	}
	t.Logf("checking %d real bookings against %d real equipment tags", len(bookings), len(equipmentTags))

	siteRows, err := db.DB.Query("SELECT site_id, timezone FROM sites")
	if err != nil {
		t.Fatalf("loading sites: %v", err)
	}
	tz := map[string]*time.Location{}
	for siteRows.Next() {
		var siteID, timezone string
		if err := siteRows.Scan(&siteID, &timezone); err != nil {
			t.Fatalf("scanning site: %v", err)
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			t.Fatalf("site %s has unparseable timezone %q: %v", siteID, timezone, err)
		}
		tz[siteID] = loc
	}
	siteRows.Close()
	siteTZ := func(siteID string) *time.Location {
		if loc, ok := tz[siteID]; ok {
			return loc
		}
		return time.UTC
	}

	// 1. Tag normalization: every booking must join to a known equipment tag.
	_, tagWarnings := availability.BuildEquipmentIndex(bookings, equipmentTags)
	if len(tagWarnings) > 0 {
		for _, w := range tagWarnings {
			t.Logf("FINDING: booking %s: %s", w.BookingID, w.Reason)
		}
		t.Fatalf("%d of %d real bookings have an asset_tag that does not normalize to any known equipment - see FINDING lines above", len(tagWarnings), len(bookings))
	}

	// 2. Date parsing: every booking's start_at/end_at must parse under its
	// site's timezone. Use a very wide, always-true query window so the
	// only way to get a warning here is an actual parse failure, not a
	// real non-conflict.
	wideStart := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	wideEnd := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	index, _ := availability.BuildEquipmentIndex(bookings, equipmentTags)
	var dateWarnings []availability.Warning
	for _, group := range index {
		_, warnings := availability.FindConflict(group, siteTZ, wideStart, wideEnd)
		dateWarnings = append(dateWarnings, warnings...)
	}
	if len(dateWarnings) > 0 {
		for _, w := range dateWarnings {
			t.Logf("FINDING: booking %s: %s", w.BookingID, w.Reason)
		}
		t.Fatalf("%d of %d real bookings have an unparseable date - see FINDING lines above", len(dateWarnings), len(bookings))
	}
}

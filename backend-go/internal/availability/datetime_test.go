package availability

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%s): %v", name, err)
	}
	return loc
}

// Scenarios 10-13: each of the four observed raw formats parses to the correct UTC instant.
func TestParseBookingTime_AllFourFormats(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	chi := mustLoc(t, "America/Chicago")

	cases := []struct {
		name string
		raw  string
		loc  *time.Location
		want time.Time
	}{
		{"space-naive", "2026-12-06 06:00", ny, time.Date(2026, 12, 6, 11, 0, 0, 0, time.UTC)},
		{"iso-z", "2026-11-15T11:00:00Z", ny, time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC)},
		{"date-only", "10/23/2026", ny, time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC)},
		{"us-ampm", "12/30/2026 07:00 AM", chi, time.Date(2026, 12, 30, 13, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseBookingTime(c.raw, c.loc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(c.want) {
				t.Fatalf("got %v, want %v", got.UTC(), c.want)
			}
		})
	}
}

// Scenario 14: naive timestamps on either side of the Nov 1 2026 US DST fall-back,
// at a Chicago-timezone site, must resolve to the correct UTC instant.
func TestParseBookingTime_DSTFallBack(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")

	before, err := ParseBookingTime("2026-10-31 07:00", chi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC); !before.Equal(want) {
		t.Fatalf("before DST: got %v, want %v (CDT, UTC-5)", before.UTC(), want)
	}

	after, err := ParseBookingTime("2026-11-02 07:00", chi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := time.Date(2026, 11, 2, 13, 0, 0, 0, time.UTC); !after.Equal(want) {
		t.Fatalf("after DST: got %v, want %v (CST, UTC-6)", after.UTC(), want)
	}
}

// Scenario 19: an unparseable date must return an error, not panic, not silently zero-value.
func TestParseBookingTime_Unparseable(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	_, err := ParseBookingTime("not-a-date-at-all", ny)
	if err == nil {
		t.Fatal("expected an error for unparseable input, got nil")
	}
}

func TestParseBookingTime_Empty(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	_, err := ParseBookingTime("", ny)
	if err == nil {
		t.Fatal("expected an error for empty input, got nil")
	}
}

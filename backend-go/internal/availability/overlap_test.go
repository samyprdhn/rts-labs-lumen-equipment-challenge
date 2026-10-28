package availability

import (
	"testing"
	"time"
)

func t_(hour, min int) time.Time {
	return time.Date(2026, 11, 12, hour, min, 0, 0, time.UTC)
}

// Scenarios 1-6: the real overlap geometries, using MC-007/BK-1119 (07:30-17:00) as the
// fixed booking and varying the query window.
func TestOverlaps(t *testing.T) {
	bookingStart, bookingEnd := t_(7, 30), t_(17, 0)

	cases := []struct {
		name            string
		queryS, queryE  time.Time
		wantOverlap     bool
	}{
		{"booking contained in query", t_(0, 0), time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), true},
		{"query ends mid-booking (partial left)", t_(0, 0), t_(10, 0), true},
		{"query starts mid-booking (partial right)", t_(10, 0), t_(23, 59), true},
		{"query contained in booking", t_(9, 0), t_(10, 0), true},
		{"adjacent before - touches booking start", t_(0, 0), t_(7, 30), false},
		{"adjacent after - touches booking end", t_(17, 0), t_(20, 0), false},
		{"entirely before", t_(0, 0), t_(1, 0), false},
		{"entirely after", t_(18, 0), t_(19, 0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Overlaps(bookingStart, bookingEnd, c.queryS, c.queryE)
			if got != c.wantOverlap {
				t.Fatalf("Overlaps(%v,%v,%v,%v) = %v, want %v", bookingStart, bookingEnd, c.queryS, c.queryE, got, c.wantOverlap)
			}
		})
	}
}

// Scenario 20 (part): a booking with end <= start must never overlap anything, and must not panic.
func TestOverlaps_InvalidBookingWindow(t *testing.T) {
	// end before start
	if Overlaps(t_(10, 0), t_(9, 0), t_(0, 0), t_(23, 59)) {
		t.Fatal("a booking with end before start must never be reported as overlapping")
	}
	// end == start (zero-duration)
	if Overlaps(t_(10, 0), t_(10, 0), t_(0, 0), t_(23, 59)) {
		t.Fatal("a zero-duration booking must never be reported as overlapping")
	}
}

package availability

import (
	"fmt"
	"strings"
	"time"
)

// The four raw timestamp shapes observed in data/bookings.csv. Formats
// without an explicit offset are interpreted as site-local wall-clock time
// (see ADR-pending A3) using loc; the Z-suffixed ISO form is always UTC
// regardless of site.
var namedLayouts = []string{
	"2006-01-02 15:04",      // "2026-12-06 06:00" (594/743 in the seed data)
	"01/02/2006",            // "10/23/2026" (date-only)
	"01/02/2006 03:04 PM",   // "12/30/2026 07:00 AM"
}

// ParseBookingTime parses one of the four observed raw formats into an
// absolute instant. Naive formats are resolved against loc (the booking's
// site timezone); the ISO-Z format is parsed as UTC directly.
func ParseBookingTime(raw string, loc *time.Location) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}

	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return t.UTC(), nil
	}

	for _, layout := range namedLayouts {
		if t, err := time.ParseInLocation(layout, trimmed, loc); err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized timestamp format: %q", raw)
}

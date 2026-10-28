// Package availability holds the conflict-detection logic shared by booking
// and FleetCare-maintenance-window checks, so there is exactly one place
// that decides what "overlaps" means.
package availability

import "time"

// Overlaps reports whether [aStart, aEnd) intersects [bStart, bEnd). Touching
// at a boundary (aEnd == bStart or bEnd == aStart) is not an overlap. A
// window with end <= start never overlaps anything.
func Overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	if !aEnd.After(aStart) || !bEnd.After(bStart) {
		return false
	}
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

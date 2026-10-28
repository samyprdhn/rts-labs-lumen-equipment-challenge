package availability

import (
	"fmt"
	"time"
)

// Booking is the subset of the bookings table this package reasons about.
type Booking struct {
	BookingID  string
	AssetTag   string
	SiteID     string
	StartAt    string
	EndAt      string
	OperatorID string
	Status     string
}

// Conflict describes the booking that makes an asset unavailable for a
// requested window.
type Conflict struct {
	BookingID  string
	SiteID     string
	StartAt    string
	EndAt      string
	Status     string
	OperatorID string
}

// Warning is a data-quality issue found while evaluating availability. It
// must never silently drop the affected booking from consideration without
// a trace - a warning is how "logged, not dropped" is implemented.
type Warning struct {
	BookingID string
	Reason    string
}

// blockingStatuses are the booking statuses that count as a conflict today
// (A1's default: confirmed and tentative; cancelled never blocks, A2).
var blockingStatuses = map[string]bool{"confirmed": true, "tentative": true}

// BuildEquipmentIndex groups bookings by normalized asset tag so lookups are
// immune to case, whitespace, and hyphen differences in the source data. Any
// booking whose tag doesn't normalize to a known equipment tag is reported
// as a warning rather than silently excluded.
func BuildEquipmentIndex(bookings []Booking, equipmentTags []string) (map[string][]Booking, []Warning) {
	known := make(map[string]bool, len(equipmentTags))
	for _, t := range equipmentTags {
		known[NormalizeTag(t)] = true
	}

	index := map[string][]Booking{}
	var warnings []Warning
	for _, b := range bookings {
		key := NormalizeTag(b.AssetTag)
		if !known[key] {
			warnings = append(warnings, Warning{
				BookingID: b.BookingID,
				Reason:    fmt.Sprintf("asset_tag %q does not match any known equipment after normalization", b.AssetTag),
			})
			continue
		}
		index[key] = append(index[key], b)
	}
	return index, warnings
}

// FindConflict returns the first blocking booking (if any) for one asset's
// bookings against [queryStart, queryEnd), using the one shared Overlaps
// test. siteTZ resolves a booking's site to the timezone its naive
// timestamps should be interpreted in. Rows with unparseable dates are
// skipped and reported as warnings, never treated as a conflict and never
// silently discarded without a trace.
func FindConflict(bookings []Booking, siteTZ func(siteID string) *time.Location, queryStart, queryEnd time.Time) (*Conflict, []Warning) {
	var warnings []Warning
	for _, b := range bookings {
		if !blockingStatuses[b.Status] {
			continue
		}

		loc := siteTZ(b.SiteID)
		if loc == nil {
			loc = time.UTC
		}

		start, err := ParseBookingTime(b.StartAt, loc)
		if err != nil {
			warnings = append(warnings, Warning{BookingID: b.BookingID, Reason: "unparseable start_at: " + err.Error()})
			continue
		}
		end, err := ParseBookingTime(b.EndAt, loc)
		if err != nil {
			warnings = append(warnings, Warning{BookingID: b.BookingID, Reason: "unparseable end_at: " + err.Error()})
			continue
		}

		if Overlaps(start, end, queryStart, queryEnd) {
			return &Conflict{
				BookingID:  b.BookingID,
				SiteID:     b.SiteID,
				StartAt:    b.StartAt,
				EndAt:      b.EndAt,
				Status:     b.Status,
				OperatorID: b.OperatorID,
			}, warnings
		}
	}
	return nil, warnings
}

package availability

import "strings"

// NormalizeTag collapses case, whitespace, and hyphen-placement differences
// so a dirty asset_tag value from bookings.csv (e.g. "mc-005", "EX-009 ",
// "DZ003") joins against the canonical equipment.asset_tag.
func NormalizeTag(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

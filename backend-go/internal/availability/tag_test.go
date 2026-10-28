package availability

import "testing"

// Scenarios 15-18: case, whitespace, and hyphen-stripped tag variants all normalize
// to the same canonical key as the real equipment tag.
func TestNormalizeTag(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"MC-005", "MC005"},
		{"mc-005", "MC005"},          // pure case mismatch (BK-1572)
		{"EX-009 ", "EX009"},         // trailing whitespace (BK-1360)
		{" ex-009", "EX009"},         // leading whitespace + case
		{"DZ003", "DZ003"},           // hyphen stripped at the source
		{"DZ-003", "DZ003"},          // canonical form strips to the same key
	}
	for _, c := range cases {
		got := NormalizeTag(c.raw)
		if got != c.want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestNormalizeTag_NoCollisionsAcrossRealFleet(t *testing.T) {
	// Every real equipment tag (from data/equipment.csv) must normalize to a
	// distinct key - if two different real tags collided, normalization itself
	// would introduce a new bug instead of fixing one.
	real := []string{
		"MC-001", "MC-002", "MC-003", "MC-004", "MC-005", "MC-006", "MC-007", "MC-008",
		"TC-001", "TC-002", "TC-003",
		"EX-001", "EX-002", "EX-003", "EX-004", "EX-005", "EX-006", "EX-007", "EX-008", "EX-009", "EX-010", "EX-011", "EX-012",
		"SS-001", "SS-002", "SS-003", "SS-004", "SS-005", "SS-006", "SS-007", "SS-008",
		"BL-001", "BL-002", "BL-003", "BL-004", "BL-005", "BL-006", "BL-007", "BL-008", "BL-009",
		"SL-001", "SL-002", "SL-003", "SL-004", "SL-005", "SL-006", "SL-007", "SL-008",
		"TH-001", "TH-002", "TH-003", "TH-004", "TH-005", "TH-006", "TH-007",
		"DZ-001", "DZ-002", "DZ-003", "DZ-004", "DZ-005",
	}
	seen := map[string]string{}
	for _, tag := range real {
		key := NormalizeTag(tag)
		if prior, ok := seen[key]; ok {
			t.Fatalf("collision: %q and %q both normalize to %q", prior, tag, key)
		}
		seen[key] = tag
	}
	if len(seen) != len(real) {
		t.Fatalf("expected %d distinct keys, got %d", len(real), len(seen))
	}
}

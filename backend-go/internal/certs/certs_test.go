package certs

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

// Scenario 27 (part): both observed date formats parse, independently per
// entry - Part 1 recon found 65% of operators mix both in one row.
func TestParseCertifications_MixedFormats(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	raw := "Excavator Operator (exp 2026-07-01); Aerial Lift (exp 2028-06-26); OSHA 30 (exp 2028-12-26); NCCCO Tower Crane (exp 03/26/2026)"
	certs, errs := ParseCertifications(raw, ny)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(certs) != 4 {
		t.Fatalf("expected 4 certs, got %d: %+v", len(certs), certs)
	}
	names := map[string]bool{}
	for _, c := range certs {
		names[c.Name] = true
	}
	for _, want := range []string{"Excavator Operator", "Aerial Lift", "OSHA 30", "NCCCO Tower Crane"} {
		if !names[want] {
			t.Fatalf("missing cert %q in %+v", want, certs)
		}
	}
}

func TestParseCertifications_SingleEntry(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	certs, errs := ParseCertifications("OSHA 30 (exp 2026-01-01)", ny)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(certs) != 1 || certs[0].Name != "OSHA 30" {
		t.Fatalf("got %+v", certs)
	}
}

func TestParseCertifications_UnrecognizedEntryIsReportedNotSilentlyDropped(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	certs, errs := ParseCertifications("garbage entry with no exp date", ny)
	if len(certs) != 0 {
		t.Fatalf("expected 0 parsed certs, got %+v", certs)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
}

func TestParseCertifications_Empty(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	certs, errs := ParseCertifications("", ny)
	if len(certs) != 0 || len(errs) != 0 {
		t.Fatalf("expected no certs and no errors for an empty field, got %+v / %v", certs, errs)
	}
}

// Scenario 21/22/23/24: expiry before / during / exactly-at / after the
// booking's end. Boundary convention (documented, not silent): expiry
// exactly equal to the booking's end counts as VALID - a cert good
// "through" a calendar day covers that day's last instant, and the
// concern (Priya: "expires in the middle of a booking") is about the cert
// running out strictly BEFORE the booking ends, not landing in exact
// lockstep with it.
func TestHasValidCert_Boundaries(t *testing.T) {
	cert := Cert{Name: "NCCCO Mobile Crane", Expires: time.Date(2026, 11, 12, 23, 59, 59, 0, time.UTC)}
	certs := []Cert{cert}

	cases := []struct {
		name  string
		until time.Time
		want  bool
	}{
		{"booking ends before expiry", time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), true},
		{"booking ends exactly at expiry", cert.Expires, true},
		{"booking ends 1s after expiry", cert.Expires.Add(time.Second), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := HasValidCert(certs, "NCCCO Mobile Crane", c.until)
			if got != c.want {
				t.Fatalf("HasValidCert(until=%v) = %v, want %v", c.until, got, c.want)
			}
		})
	}
}

func TestHasValidCert_WrongCertType(t *testing.T) {
	certs := []Cert{{Name: "Dozer Operator", Expires: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}
	if HasValidCert(certs, "NCCCO Mobile Crane", time.Now()) {
		t.Fatal("a Dozer Operator cert must not satisfy an NCCCO Mobile Crane requirement")
	}
}

func TestHasValidCert_NoCerts(t *testing.T) {
	if HasValidCert(nil, "NCCCO Mobile Crane", time.Now()) {
		t.Fatal("no certs at all must never satisfy a requirement")
	}
}

// Expiry date interpreted as end-of-day in the given site's timezone (B3) -
// verify the actual computed instant, not just that parsing succeeds.
func TestParseCertifications_ExpiryIsEndOfDayInSiteTimezone(t *testing.T) {
	chi := mustLoc(t, "America/Chicago")
	certs, errs := ParseCertifications("Dozer Operator (exp 2026-11-12)", chi)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// 2026-11-12 23:59:59 CST (UTC-6, standard time in November) = 2026-11-13 05:59:59 UTC
	want := time.Date(2026, 11, 13, 5, 59, 59, 0, time.UTC)
	if !certs[0].Expires.Equal(want) {
		t.Fatalf("got expiry %v, want %v", certs[0].Expires.UTC(), want)
	}
}

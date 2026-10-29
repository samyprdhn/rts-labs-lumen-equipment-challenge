// Package certs parses the operators.certifications column and answers
// whether a given certification covers a booking's whole window.
package certs

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Cert is one parsed certification entry.
type Cert struct {
	Name    string
	Expires time.Time // the certification's last valid instant, in UTC
}

var entryPattern = regexp.MustCompile(`^(.*?)\s*\(exp\s+([^)]+)\)$`)

// dateLayouts are the two formats observed in data/operators.csv - Part 1
// recon found 75 ISO entries and 40 US entries across 115 total, with 26 of
// 40 operators mixing both within a single row, so this is parsed per
// entry, never assumed uniform across a row.
var dateLayouts = []string{"2006-01-02", "01/02/2006"}

// ParseCertifications parses a semicolon-delimited certifications field,
// e.g. "OSHA 30 (exp 2026-01-01); Dozer Operator (exp 02/13/2027)". loc is
// the timezone an expiry date is interpreted in - callers pass the booking
// site's timezone (B3), not the operator's home_region, since the question
// being asked is "valid through the end of this booking," and the booking
// happens at a specific site.
//
// Unrecognized entries are reported as errors, not silently dropped - a
// row this function can't parse should be visible, not invisible.
func ParseCertifications(raw string, loc *time.Location) ([]Cert, []error) {
	var out []Cert
	var errs []error
	for _, entry := range strings.Split(raw, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		m := entryPattern.FindStringSubmatch(entry)
		if m == nil {
			errs = append(errs, fmt.Errorf("unrecognized certification entry: %q", entry))
			continue
		}
		name := strings.TrimSpace(m[1])
		expiry, err := parseExpiryEndOfDay(m[2], loc)
		if err != nil {
			errs = append(errs, fmt.Errorf("certification %q: %w", name, err))
			continue
		}
		out = append(out, Cert{Name: name, Expires: expiry})
	}
	return out, errs
}

// parseExpiryEndOfDay parses a date-only expiry string and returns the last
// instant of that calendar day in loc, converted to UTC - a cert "good
// through 2026-01-01" covers all of that day, not just its first instant.
func parseExpiryEndOfDay(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			endOfDay := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, loc)
			return endOfDay.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized expiry date format: %q", raw)
}

// HasValidCert reports whether certs contains an entry named exactly
// requiredCert whose expiry covers until - the booking's end instant.
//
// Boundary, documented explicitly (not silently chosen): expiry exactly
// equal to until counts as valid. A cert good "through" a calendar day
// covers that day's last instant; Priya's stated concern is a cert running
// out strictly before a booking ends ("expires in the middle of a
// booking"), not one that ends in exact lockstep with it.
func HasValidCert(certsHeld []Cert, requiredCert string, until time.Time) bool {
	for _, c := range certsHeld {
		if c.Name == requiredCert && !c.Expires.Before(until) {
			return true
		}
	}
	return false
}

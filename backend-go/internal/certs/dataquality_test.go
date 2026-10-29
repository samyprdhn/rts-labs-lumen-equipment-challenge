package certs_test

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lumen/equipment-api/internal/certs"
)

// Runs the cert parser against the full real data/operators.csv (40 rows,
// 115 certification entries per Part 1 recon) - not a synthetic fixture.
// If the real data violates an assumption this parser makes, this test
// reports which row and why, rather than hiding it.
func TestParseCertifications_FullRealDataset(t *testing.T) {
	repoRoot := os.Getenv("REPO_ROOT")
	if repoRoot == "" {
		repoRoot = "../../.."
	}
	f, err := os.Open(filepath.Join(repoRoot, "data", "operators.csv"))
	if err != nil {
		t.Fatalf("opening data/operators.csv: %v", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("reading data/operators.csv: %v", err)
	}
	if len(rows) < 2 {
		t.Fatal("expected a header row plus data rows")
	}

	header := rows[0]
	certCol := -1
	for i, h := range header {
		if h == "certifications" {
			certCol = i
		}
	}
	if certCol == -1 {
		t.Fatal("no certifications column in data/operators.csv")
	}

	loc, _ := time.LoadLocation("America/New_York")
	total := 0
	var failures []string
	for _, row := range rows[1:] {
		opID := row[0]
		parsed, errs := certs.ParseCertifications(row[certCol], loc)
		total += len(parsed)
		for _, e := range errs {
			failures = append(failures, opID+": "+e.Error())
		}
	}
	t.Logf("parsed %d certification entries across %d real operators", total, len(rows)-1)

	if len(failures) > 0 {
		for _, f := range failures {
			t.Logf("FINDING: %s", f)
		}
		t.Fatalf("%d certification entries in the real data failed to parse - see FINDING lines above", len(failures))
	}
}

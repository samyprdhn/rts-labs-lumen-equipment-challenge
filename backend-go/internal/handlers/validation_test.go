package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lumen/equipment-api/internal/server"
)

func opsToken() string {
	return token(map[string]any{"sub": "u-ops-01", "role": "ops_director", "site_ids": []string{"*"}})
}

func rawRequest(qs string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/equipment/availability?"+qs, nil)
	req.Header.Set("Authorization", "Bearer "+opsToken())
	rec := httptest.NewRecorder()
	server.New().ServeHTTP(rec, req)
	return rec
}

// Scenario 1/2: missing start or end must 400 with an actionable message,
// not silently degrade to "everything available".
func TestValidation_MissingStartOrEnd(t *testing.T) {
	for _, qs := range []string{"type=mobile_crane&end=2026-10-15T00:00:00Z", "type=mobile_crane&start=2026-10-13T00:00:00Z"} {
		rec := rawRequest(qs)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("qs=%q: expected 400, got %d: %s", qs, rec.Code, rec.Body.String())
		}
		var body map[string]any
		json.Unmarshal(rec.Body.Bytes(), &body)
		if body["error"] == nil || body["error"] == "" {
			t.Fatalf("qs=%q: expected a non-empty error message, got %+v", qs, body)
		}
	}
}

// Scenario 3: malformed dates must 400.
func TestValidation_MalformedDates(t *testing.T) {
	rec := rawRequest("start=not-a-date&end=2026-10-15T00:00:00Z")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Scenario 4: end <= start must 400 with a message naming the problem.
func TestValidation_EndBeforeStart(t *testing.T) {
	rec := rawRequest("start=2026-10-15T00:00:00Z&end=2026-10-13T00:00:00Z")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	msg, _ := body["error"].(string)
	if msg == "" {
		t.Fatal("expected a non-empty error message")
	}
}

// Scenario 5: valid params still work (regression).
func TestValidation_ValidParamsStillWork(t *testing.T) {
	rec := rawRequest("type=mobile_crane&start=2026-10-13T00:00:00Z&end=2026-10-15T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Scenario 6: the increment-1 SQLi fix still holds with validation added.
func TestValidation_SQLiStillRejected(t *testing.T) {
	rec := rawRequest("type=mobile_crane%27%20OR%20%271%27%3D%271&start=2026-10-13T00:00:00Z&end=2026-10-15T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Data) >= 60 {
		t.Fatalf("expected the injection to return 0 or few rows, got %d (looks like all equipment)", len(body.Data))
	}
}

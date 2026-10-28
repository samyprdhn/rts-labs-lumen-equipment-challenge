package handlers_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"lumen/equipment-api/internal/config"
	"lumen/equipment-api/internal/db"
	"lumen/equipment-api/internal/server"
)

// NOTE: requires `npm run seed` from the repo root first.

func TestMain(m *testing.M) {
	os.Setenv("REPO_ROOT", "../../..")
	config.LoadEnv()
	db.Open()
	os.Exit(m.Run())
}

func token(claims map[string]any) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	unsigned := enc.EncodeToString(h) + "." + enc.EncodeToString(p)
	mac := hmac.New(sha256.New, []byte(os.Getenv("JWT_SECRET")))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func TestAvailabilityRequiresToken(t *testing.T) {
	rec := httptest.NewRecorder()
	server.New().ServeHTTP(rec, httptest.NewRequest("GET", "/api/equipment/availability", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAvailabilityReturnsCranes(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/equipment/availability?type=mobile_crane&start=2026-10-13&end=2026-10-15", nil)
	req.Header.Set("Authorization", "Bearer "+token(map[string]any{"sub": "u-ops-01", "role": "ops_director", "site_ids": []string{"*"}}))
	rec := httptest.NewRecorder()
	server.New().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Data) == 0 {
		t.Fatal("expected some cranes")
	}
	for _, e := range body.Data {
		if e["type"] != "mobile_crane" {
			t.Fatalf("unexpected type %v", e["type"])
		}
	}
}

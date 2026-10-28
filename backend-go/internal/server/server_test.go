package server_test

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"lumen/equipment-api/internal/config"
	"lumen/equipment-api/internal/db"
	"lumen/equipment-api/internal/fleetcare"
	"lumen/equipment-api/internal/handlers"
	"lumen/equipment-api/internal/server"
)

func TestMain(m *testing.M) {
	os.Setenv("REPO_ROOT", "../../..")
	config.LoadEnv()
	db.Open()
	handlers.InitFleetCareWithFetcher(noopFetcher{}, 0, 0)
	os.Exit(m.Run())
}

type noopFetcher struct{}

func (noopFetcher) FetchAll(_ context.Context, tags []string) (map[string][]fleetcare.Window, error) {
	return nil, nil
}

// Scenario 7: a panic in a downstream handler must never leak a stack trace
// or a file path to the client.
func TestRecoverErrors_NoStackTraceToClient(t *testing.T) {
	panicky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom: something internal broke")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	server.Wrap(panicky).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, ".go:") || strings.Contains(strings.ToLower(body), "goroutine") {
		t.Fatalf("response body leaks a stack trace: %s", body)
	}
}

// Scenario 8: the server's own request log must never contain the
// Authorization header or a bearer token.
func TestLogRequests_NeverLogsAuthorization(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	handler := server.New()
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("Authorization", "Bearer super-secret-token-value")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logged := buf.String()
	if strings.Contains(logged, "super-secret-token-value") || strings.Contains(logged, "Authorization") {
		t.Fatalf("request log leaked the Authorization header: %s", logged)
	}
}

// Scenario 9/10: CORS reflects an explicitly allowed origin and omits the
// header entirely for anything else - never a bare wildcard with credentials.
func TestCORS_AllowedVsOther(t *testing.T) {
	handler := server.New()

	allowed := httptest.NewRequest("GET", "/healthz", nil)
	allowed.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, allowed)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("expected the allowed origin reflected, got %q", got)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Fatal("must never be a bare wildcard")
	}

	other := httptest.NewRequest("GET", "/healthz", nil)
	other.Header.Set("Origin", "http://evil.example")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, other)
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header for a non-allowed origin, got %q", got)
	}
}

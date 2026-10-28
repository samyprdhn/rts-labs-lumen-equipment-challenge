package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testSecret = "test-secret-do-not-use-in-prod"

func sign(secret string, header, payload map[string]any) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(header)
	p, _ := json.Marshal(payload)
	unsigned := enc.EncodeToString(h) + "." + enc.EncodeToString(p)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func validClaims() map[string]any {
	return map[string]any{
		"sub": "u-ops-01", "role": "ops_director", "site_ids": []string{"*"},
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

func callWithToken(t *testing.T, token string) int {
	t.Helper()
	var gotStatus int
	handler := RequireAuth(testSecret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	handler.ServeHTTP(rec, req)
	gotStatus = rec.Code
	return gotStatus
}

// Scenario 1: a validly-signed, unexpired token is accepted.
func TestRequireAuth_ValidToken(t *testing.T) {
	token := sign(testSecret, map[string]any{"alg": "HS256", "typ": "JWT"}, validClaims())
	if got := callWithToken(t, token); got != http.StatusOK {
		t.Fatalf("expected 200, got %d", got)
	}
}

// Scenario 2: swapping the payload segment (e.g. an attacker trying to
// escalate role to ops_director) while keeping the original signature must
// be rejected - the signature no longer matches the new payload.
func TestRequireAuth_TamperedPayload(t *testing.T) {
	foremanClaims := map[string]any{
		"sub": "u-for-01", "role": "foreman", "site_ids": []string{"S01"},
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	legit := sign(testSecret, map[string]any{"alg": "HS256", "typ": "JWT"}, foremanClaims)
	parts := strings.Split(legit, ".")

	escalated := map[string]any{
		"sub": "u-for-01", "role": "ops_director", "site_ids": []string{"*"},
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	newPayload, _ := json.Marshal(escalated)
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(newPayload) + "." + parts[2]

	if got := callWithToken(t, tampered); got != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a payload swapped without re-signing, got %d", got)
	}
}

// Scenario 3: an expired token is rejected.
func TestRequireAuth_Expired(t *testing.T) {
	claims := validClaims()
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	token := sign(testSecret, map[string]any{"alg": "HS256", "typ": "JWT"}, claims)
	if got := callWithToken(t, token); got != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", got)
	}
}

// Scenario 4: a token with no exp claim at all must be rejected, not
// treated as never-expiring.
func TestRequireAuth_MissingExp(t *testing.T) {
	claims := map[string]any{"sub": "u-ops-01", "role": "ops_director", "site_ids": []string{"*"}}
	token := sign(testSecret, map[string]any{"alg": "HS256", "typ": "JWT"}, claims)
	if got := callWithToken(t, token); got != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token with no exp claim, got %d", got)
	}
}

// Scenario 5: a 0-dot garbage token must 401, not panic.
func TestRequireAuth_NoDots(t *testing.T) {
	if got := callWithToken(t, "totallynotatoken"); got != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", got)
	}
}

// Scenario 6: a 2-dot garbage token must 401, not be silently accepted.
// This is the exact live defect found earlier: json.Unmarshal's discarded
// error let a zero-value User through with a 200.
func TestRequireAuth_GarbageButJWTShaped(t *testing.T) {
	if got := callWithToken(t, "not.a.jwt"); got != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", got)
	}
}

// Scenario 7: an empty-but-present bearer value must 401, not 500.
func TestRequireAuth_EmptyBearerValue(t *testing.T) {
	handler := RequireAuth(testSecret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// Scenario 8: a fully missing Authorization header must 401 (regression).
func TestRequireAuth_MissingHeader(t *testing.T) {
	if got := callWithToken(t, ""); got != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", got)
	}
}

// Scenario 9: a token whose header claims an algorithm other than HS256
// must be rejected outright - alg-confusion/alg:none pinning.
func TestRequireAuth_WrongAlgorithmRejected(t *testing.T) {
	token := sign(testSecret, map[string]any{"alg": "none", "typ": "JWT"}, validClaims())
	if got := callWithToken(t, token); got != http.StatusUnauthorized {
		t.Fatalf("expected 401 for alg=none, got %d", got)
	}
}

// Scenario 9b: signed with a DIFFERENT secret must be rejected (sanity check
// that verification actually checks the signature against OUR secret).
func TestRequireAuth_WrongSecret(t *testing.T) {
	token := sign("some-other-secret", map[string]any{"alg": "HS256", "typ": "JWT"}, validClaims())
	if got := callWithToken(t, token); got != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token signed with the wrong secret, got %d", got)
	}
}

// UserFrom must still expose the validated claims to downstream handlers.
func TestRequireAuth_PopulatesUser(t *testing.T) {
	var captured *User
	handler := RequireAuth(testSecret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = UserFrom(r)
		w.WriteHeader(http.StatusOK)
	}))
	token := sign(testSecret, map[string]any{"alg": "HS256", "typ": "JWT"}, validClaims())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if captured == nil || captured.Role != "ops_director" || captured.Sub != "u-ops-01" {
		t.Fatalf("expected a populated User, got %+v", captured)
	}
}

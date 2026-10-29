package auth_test

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

	"lumen/equipment-api/internal/auth"
)

const testSecret = "test-secret-for-auth-tests-32-bytes-minimum"

var testConfig = auth.Config{
	Secret:   testSecret,
	Issuer:   "lumen-dev",
	Audience: "lumen-equipment-api",
}

// sign creates a signed JWT for testing.
func sign(secret string, header, payload map[string]any) string {
	enc := base64.RawURLEncoding

	h, err := json.Marshal(header)
	if err != nil {
		panic(err)
	}

	p, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}

	unsigned := enc.EncodeToString(h) + "." + enc.EncodeToString(p)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))

	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func validClaims() map[string]any {
	return map[string]any{
		"iss":      "lumen-dev",
		"aud":      "lumen-equipment-api",
		"sub":      "u-ops-01",
		"name":     "Dana Whitaker",
		"role":     "ops_director",
		"site_ids": []string{"*"},
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(time.Hour).Unix(),
	}
}

func validToken() string {
	return sign(
		testSecret,
		map[string]any{"alg": "HS256", "typ": "JWT"},
		validClaims(),
	)
}

// callWith sends a request through RequireAuth and returns its response.
// The inner handler returns 200 if authentication succeeds.
func callWith(t *testing.T, cfg auth.Config, authHeader string) int {
	t.Helper()

	h := auth.RequireAuth(cfg, http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	))

	req := httptest.NewRequest("GET", "/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code
}

func TestRequireAuth_ValidToken(t *testing.T) {
	tok := validToken()

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusOK {
		t.Errorf("valid token: got %d, want 200", code)
	}
}

func TestRequireAuth_MissingHeader(t *testing.T) {
	if code := callWith(t, testConfig, ""); code != http.StatusUnauthorized {
		t.Errorf("missing header: got %d, want 401", code)
	}
}

func TestRequireAuth_WrongScheme(t *testing.T) {
	tok := validToken()

	if code := callWith(t, testConfig, "Basic "+tok); code != http.StatusUnauthorized {
		t.Errorf("wrong scheme: got %d, want 401", code)
	}
}

func TestRequireAuth_BearerSchemeCaseInsensitive(t *testing.T) {
	tok := validToken()

	if code := callWith(t, testConfig, "bEaReR "+tok); code != http.StatusOK {
		t.Errorf("case-insensitive Bearer: got %d, want 200", code)
	}
}

// Changing the payload without updating the signature must be rejected.
func TestRequireAuth_TamperedPayloadKeepsStaleSignature(t *testing.T) {
	tok := validToken()
	parts := strings.Split(tok, ".")

	forgedPayload := map[string]any{
		"iss":      "lumen-dev",
		"aud":      "lumen-equipment-api",
		"sub":      "u-forged",
		"name":     "Forged User",
		"role":     "ops_director",
		"site_ids": []string{"*"},
		"exp":      time.Now().Add(time.Hour).Unix(),
	}

	p, err := json.Marshal(forgedPayload)
	if err != nil {
		t.Fatal(err)
	}

	forged := parts[0] + "." +
		base64.RawURLEncoding.EncodeToString(p) + "." +
		parts[2]

	if code := callWith(t, testConfig, "Bearer "+forged); code != http.StatusUnauthorized {
		t.Errorf("tampered payload: got %d, want 401", code)
	}
}

func TestRequireAuth_WrongSecret(t *testing.T) {
	tok := sign(
		"attacker-controlled-secret",
		map[string]any{"alg": "HS256", "typ": "JWT"},
		validClaims(),
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusUnauthorized {
		t.Errorf("wrong secret: got %d, want 401", code)
	}
}

func TestRequireAuth_AlgNoneRejected(t *testing.T) {
	tok := sign(
		testSecret,
		map[string]any{"alg": "none", "typ": "JWT"},
		validClaims(),
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusUnauthorized {
		t.Errorf("alg=none: got %d, want 401", code)
	}
}

func TestRequireAuth_WrongAlgorithmRejected(t *testing.T) {
	tok := sign(
		testSecret,
		map[string]any{"alg": "HS384", "typ": "JWT"},
		validClaims(),
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusUnauthorized {
		t.Errorf("wrong algorithm: got %d, want 401", code)
	}
}

func TestRequireAuth_Expired(t *testing.T) {
	claims := validClaims()
	claims["exp"] = time.Now().Add(-time.Hour).Unix()

	tok := sign(
		testSecret,
		map[string]any{"alg": "HS256", "typ": "JWT"},
		claims,
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusUnauthorized {
		t.Errorf("expired token: got %d, want 401", code)
	}
}

func TestRequireAuth_MissingExpiration(t *testing.T) {
	claims := validClaims()
	delete(claims, "exp")

	tok := sign(
		testSecret,
		map[string]any{"alg": "HS256", "typ": "JWT"},
		claims,
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusUnauthorized {
		t.Errorf("missing exp: got %d, want 401", code)
	}
}

func TestRequireAuth_FutureNotBefore(t *testing.T) {
	claims := validClaims()
	claims["nbf"] = time.Now().Add(time.Hour).Unix()

	tok := sign(
		testSecret,
		map[string]any{"alg": "HS256", "typ": "JWT"},
		claims,
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusUnauthorized {
		t.Errorf("future nbf: got %d, want 401", code)
	}
}

func TestRequireAuth_WrongIssuer(t *testing.T) {
	claims := validClaims()
	claims["iss"] = "someone-else"

	tok := sign(
		testSecret,
		map[string]any{"alg": "HS256", "typ": "JWT"},
		claims,
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusUnauthorized {
		t.Errorf("wrong issuer: got %d, want 401", code)
	}
}

func TestRequireAuth_WrongAudience(t *testing.T) {
	claims := validClaims()
	claims["aud"] = "someone-elses-api"

	tok := sign(
		testSecret,
		map[string]any{"alg": "HS256", "typ": "JWT"},
		claims,
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusUnauthorized {
		t.Errorf("wrong audience: got %d, want 401", code)
	}
}

func TestRequireAuth_AudienceArray(t *testing.T) {
	claims := validClaims()
	claims["aud"] = []string{
		"another-api",
		"lumen-equipment-api",
	}

	tok := sign(
		testSecret,
		map[string]any{"alg": "HS256", "typ": "JWT"},
		claims,
	)

	if code := callWith(t, testConfig, "Bearer "+tok); code != http.StatusOK {
		t.Errorf("valid audience array: got %d, want 200", code)
	}
}

func TestRequireAuth_MalformedGarbage(t *testing.T) {
	if code := callWith(t, testConfig, "Bearer garbage.notbase64json.xyz"); code != http.StatusUnauthorized {
		t.Errorf("malformed token: got %d, want 401", code)
	}
}

func TestRequireAuth_SingleSegmentDoesNotPanic(t *testing.T) {
	if code := callWith(t, testConfig, "Bearer sometoken"); code != http.StatusUnauthorized {
		t.Errorf("single-segment token: got %d, want 401", code)
	}
}

func TestRequireAuth_InvalidConfigurationPanics(t *testing.T) {
	tests := []struct {
		name string
		cfg  auth.Config
	}{
		{
			name: "missing secret",
			cfg: auth.Config{
				Issuer:   "lumen-dev",
				Audience: "lumen-equipment-api",
			},
		},
		{
			name: "short secret",
			cfg: auth.Config{
				Secret:   "too-short",
				Issuer:   "lumen-dev",
				Audience: "lumen-equipment-api",
			},
		},
		{
			name: "missing issuer",
			cfg: auth.Config{
				Secret:   testSecret,
				Audience: "lumen-equipment-api",
			},
		},
		{
			name: "missing audience",
			cfg: auth.Config{
				Secret: testSecret,
				Issuer: "lumen-dev",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected RequireAuth to panic for invalid configuration")
				}
			}()

			auth.RequireAuth(tt.cfg, http.HandlerFunc(
				func(http.ResponseWriter, *http.Request) {},
			))
		})
	}
}

func TestRequireAuth_UserAvailableInContext(t *testing.T) {
	tok := validToken()
	called := false

	h := auth.RequireAuth(testConfig, http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			called = true

			u := auth.UserFrom(r)
			if u == nil {
				t.Fatal("expected authenticated user in request context")
			}

			if u.Sub != "u-ops-01" {
				t.Errorf("sub: got %q, want %q", u.Sub, "u-ops-01")
			}

			if u.Role != "ops_director" {
				t.Errorf("role: got %q, want %q", u.Role, "ops_director")
			}

			if len(u.SiteIDs) != 1 || u.SiteIDs[0] != "*" {
				t.Errorf("site_ids: got %v, want [*]", u.SiteIDs)
			}

			w.WriteHeader(http.StatusOK)
		},
	))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected downstream handler to be called")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
}

func TestCanAccessSite_WildcardMatchesAnySite(t *testing.T) {
	u := &auth.User{SiteIDs: []string{"*"}}

	if !u.CanAccessSite("S07") {
		t.Fatal("expected wildcard site_ids to match any site")
	}
}

func TestCanAccessSite_ExactMatch(t *testing.T) {
	u := &auth.User{SiteIDs: []string{"S01", "S02"}}

	if !u.CanAccessSite("S02") {
		t.Fatal("expected S02 to match")
	}
}

func TestCanAccessSite_NoMatch(t *testing.T) {
	u := &auth.User{SiteIDs: []string{"S01", "S02"}}

	if u.CanAccessSite("S09") {
		t.Fatal("expected S09 not to match")
	}
}

func TestCanAccessSite_EmptySiteIDs(t *testing.T) {
	u := &auth.User{SiteIDs: nil}

	if u.CanAccessSite("S01") {
		t.Fatal("expected empty site_ids to match nothing")
	}
}

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type User struct {
	Sub     string   `json:"sub"`
	Name    string   `json:"name"`
	Role    string   `json:"role"`
	SiteIDs []string `json:"site_ids"`
}

// Config holds everything RequireAuth needs. Load it once at startup
// (e.g. Secret from JWT_SECRET, Issuer "lumen-dev", Audience
// "lumen-equipment-api") and pass it in, so tests can inject their own
// values and a misconfigured deploy fails on boot instead of per request.
type Config struct {
	Secret   string
	Issuer   string
	Audience string
}

// minSecretLen is the shortest HS256 secret we accept. Shorter secrets can
// be brute-forced offline from any captured token.
const minSecretLen = 32

// clockSkew tolerates small clock differences between the token issuer
// and this server when checking exp and nbf.
const clockSkew = 30 * time.Second

type jwtHeader struct {
	Alg  string   `json:"alg"`
	Crit []string `json:"crit"`
}

type claims struct {
	Iss     string   `json:"iss"`
	Aud     audience `json:"aud"`
	Sub     string   `json:"sub"`
	Name    string   `json:"name"`
	Role    string   `json:"role"`
	SiteIDs []string `json:"site_ids"`
	Exp     *int64   `json:"exp"`
	Nbf     *int64   `json:"nbf"`
}

// audience accepts the JWT "aud" claim as either a single string or an
// array of strings, both of which the spec allows.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		*a = audience{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func (a audience) contains(want string) bool {
	for _, v := range a {
		if v == want {
			return true
		}
	}
	return false
}

type ctxKey struct{}

// RequireAuth verifies the bearer JWT's HMAC-SHA256 signature, algorithm,
// expiry, not-before, issuer and audience before trusting any of its
// claims (see docs/reviews/2026-10-28-part1-security.md).
//
// It panics at construction time if cfg is unusable (secret missing or
// shorter than 32 bytes, issuer or audience empty), so a misconfigured
// deploy crashes on boot rather than rejecting everyone or, worse,
// verifying tokens against an empty key.
//
// Every request-time failure (missing header, wrong scheme, malformed
// token, bad signature, wrong algorithm, expired, not yet valid, wrong
// issuer/audience) gets the same 401 and generic message, so a caller
// can't learn why a token was rejected.
func RequireAuth(cfg Config, next http.Handler) http.Handler {
	if len(cfg.Secret) < minSecretLen {
		panic("auth: JWT secret missing or shorter than 32 bytes")
	}
	if cfg.Issuer == "" || cfg.Audience == "" {
		panic("auth: JWT issuer and audience must be configured")
	}
	secret := []byte(cfg.Secret)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := verify(r.Header.Get("Authorization"), secret, cfg.Issuer, cfg.Audience, time.Now())
		if !ok {
			http.Error(w, `{"error":"invalid or missing token"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

func verify(authHeader string, secret []byte, issuer, aud string, now time.Time) (*User, bool) {
	// The auth scheme is case-insensitive per RFC 7235, and a header with
	// no scheme at all is rejected rather than treated as a raw token.
	scheme, token, found := strings.Cut(authHeader, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return nil, false
	}
	token = strings.TrimSpace(token)

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}
	var h jwtHeader
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return nil, false
	}
	if h.Alg != "HS256" {
		// Reject "none" and anything else outright; never let the token
		// itself pick an algorithm that skips verification.
		return nil, false
	}
	if len(h.Crit) > 0 {
		// We don't implement any critical header extensions, and RFC 7515
		// says a token that requires one we don't understand is invalid.
		return nil, false
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(mac.Sum(nil), sig) {
		return nil, false
	}

	// Only now, with the signature verified, is the payload trusted.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var c claims
	if err := json.Unmarshal(payloadJSON, &c); err != nil {
		return nil, false
	}
	if c.Iss != issuer || !c.Aud.contains(aud) {
		return nil, false
	}

	nowUnix := now.Unix()
	skew := int64(clockSkew / time.Second)
	if c.Exp == nil || nowUnix >= *c.Exp+skew {
		return nil, false
	}
	if c.Nbf != nil && nowUnix+skew < *c.Nbf {
		return nil, false
	}

	return &User{Sub: c.Sub, Name: c.Name, Role: c.Role, SiteIDs: c.SiteIDs}, true
}

func UserFrom(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKey{}).(*User)
	return u
}

// CanAccessSite reports whether the user's site_ids claim covers siteID,
// honoring the "*" wildcard scripts/dev-token.mjs issues for company-wide
// roles (ops_director, safety_manager). Role itself is checked separately
// (see docs/plan/02-reservations.md assumption #8); this only answers the
// "which sites" half of authorization, not the "which roles" half.
func (u *User) CanAccessSite(siteID string) bool {
	for _, s := range u.SiteIDs {
		if s == "*" || s == siteID {
			return true
		}
	}
	return false
}

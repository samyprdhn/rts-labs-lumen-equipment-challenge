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

type ctxKey struct{}

// RequireAuth verifies the bearer token's HMAC-SHA256 signature against
// secret, pins the algorithm to exactly HS256 (rejecting "none" and any
// other alg-confusion attempt), and requires a valid, unexpired `exp`
// claim. Any failure - missing header, wrong number of segments, bad
// base64, bad JSON, wrong algorithm, bad signature, missing or expired
// exp - returns a plain 401 with no detail about which check failed, and
// never panics and never lets the request through unauthenticated.
func RequireAuth(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			unauthorized(w)
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")

		user, ok := verify(token, secret)
		if !ok {
			unauthorized(w)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

func unauthorized(w http.ResponseWriter) {
	http.Error(w, `{"error":"missing or invalid token"}`, http.StatusUnauthorized)
}

func verify(token, secret string) (*User, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, false
	}
	if header.Alg != "HS256" {
		return nil, false
	}

	expectedSig := hmac.New(sha256.New, []byte(secret))
	expectedSig.Write([]byte(parts[0] + "." + parts[1]))
	expected := expectedSig.Sum(nil)

	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, false
	}
	if !hmac.Equal(expected, actual) {
		return nil, false
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims struct {
		User
		Exp *int64 `json:"exp"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, false
	}
	if claims.Exp == nil {
		return nil, false
	}
	if time.Now().Unix() >= *claims.Exp {
		return nil, false
	}

	u := claims.User
	return &u, true
}

func UserFrom(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKey{}).(*User)
	return u
}

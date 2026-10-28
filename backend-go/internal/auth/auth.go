package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

type User struct {
	Sub     string   `json:"sub"`
	Name    string   `json:"name"`
	Role    string   `json:"role"`
	SiteIDs []string `json:"site_ids"`
}

type ctxKey struct{}

// RequireAuth reads the user from the bearer token.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")
		parts := strings.Split(token, ".")
		payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var u User
		json.Unmarshal(payload, &u)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, &u)))
	})
}

func UserFrom(r *http.Request) *User {
	u, _ := r.Context().Value(ctxKey{}).(*User)
	return u
}

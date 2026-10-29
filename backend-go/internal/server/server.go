package server

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
	"strings"

	"lumen/equipment-api/internal/auth"
	"lumen/equipment-api/internal/config"
	"lumen/equipment-api/internal/handlers"
)

func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	})
	authCfg := auth.Config{
		Secret:   config.Get("JWT_SECRET", ""),
		Issuer:   config.Get("JWT_ISSUER", "lumen-dev"),
		Audience: config.Get("JWT_AUDIENCE", "lumen-equipment-api"),
	}
	mux.Handle("GET /api/equipment/availability", auth.RequireAuth(authCfg, http.HandlerFunc(handlers.Availability)))
	mux.Handle("POST /api/reservations", auth.RequireAuth(authCfg, http.HandlerFunc(handlers.CreateReservation)))
	// A custom Authorization header always triggers a CORS preflight
	// (it isn't a "simple" header per the Fetch spec); without an OPTIONS
	// response the browser never sends the real GET/POST at all. No auth
	// here - preflight requests never carry credentials.
	mux.HandleFunc("OPTIONS /api/equipment/availability", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("OPTIONS /api/reservations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	return Wrap(mux)
}

// Wrap applies the same middleware chain New() uses, so tests can exercise
// cors/logRequests/recoverErrors against an arbitrary handler without
// standing up the full route table.
func Wrap(next http.Handler) http.Handler {
	return cors(logRequests(recoverErrors(limitBody(next))))
}

// maxRequestBodyBytes bounds every request body, not just POST /api/reservations's -
// unbounded by default, a client body is read entirely into memory by
// json.Decode before any validation runs (docs/reviews/2026-10-28-part2-reservations.md).
// 1 MiB is generously larger than any real request this API accepts.
const maxRequestBodyBytes = 1 << 20

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// allowedOrigins is a small, explicit allow-list rather than "*" - a
// wildcard combined with Allow-Credentials is both an invalid combination
// per the CORS spec and an unnecessary exposure. CORS_ALLOWED_ORIGINS is a
// comma-separated override for other environments (e.g. a deployed
// frontend origin); the dev server origin is the default.
func allowedOrigins() []string {
	raw := config.Get("CORS_ALLOWED_ORIGINS", "http://localhost:5173")
	var out []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

func cors(next http.Handler) http.Handler {
	allowed := allowedOrigins()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		for _, a := range allowed {
			if origin == a {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Max-Age", "600")
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}

// redactedHeaders returns r.Header with Authorization removed, for logging.
// Request metadata (method, path, user agent, etc.) is useful for
// debugging; the bearer token itself must never reach a log line.
func redactedHeaders(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		if strings.EqualFold(k, "Authorization") {
			continue
		}
		out[k] = v
	}
	return out
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safe, _ := json.Marshal(redactedHeaders(r.Header))
		log.Println(r.Method, r.URL.String(), string(safe))
		next.ServeHTTP(w, r)
	})
}

// recoverErrors turns a panic into a generic 500. The full error and stack
// trace are logged server-side (never containing the Authorization header -
// logRequests redacts it independently of this) but never sent to the
// client; the inherited code returned both the raw error message and a
// full stack trace in the response body.
func recoverErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic handling %s %s: %v\n%s", r.Method, r.URL.String(), err, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

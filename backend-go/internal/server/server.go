package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"

	"lumen/equipment-api/internal/auth"
	"lumen/equipment-api/internal/handlers"
)

func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	})
	mux.Handle("GET /api/equipment/availability", auth.RequireAuth(http.HandlerFunc(handlers.Availability)))

	return cors(logRequests(recoverErrors(mux)))
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, _ := json.Marshal(r.Header)
		log.Println(r.Method, r.URL.String(), string(h))
		next.ServeHTTP(w, r)
	})
}

func recoverErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Println(err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{
					"error": fmt.Sprint(err),
					"stack": string(debug.Stack()),
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

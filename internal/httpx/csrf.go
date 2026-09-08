package httpx

import (
	"net/http"
	"strings"

	"github.com/irairdon/gritual/internal/config"
)

func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && config.OriginAllowed(origin, allowedOrigins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				w.Header().Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func OriginCSRF(allowedOrigins []string, cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bearerAuth(r) {
				next.ServeHTTP(w, r)
				return
			}
			if !mutating(r.Method) {
				if origin := r.Header.Get("Origin"); origin != "" && hasCookie(r, cookieName) && !config.OriginAllowed(origin, allowedOrigins) {
					WriteError(w, http.StatusForbidden, "forbidden", "invalid origin")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if !hasCookie(r, cookieName) {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" || !config.OriginAllowed(origin, allowedOrigins) {
				WriteError(w, http.StatusForbidden, "forbidden", "invalid origin")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerAuth(r *http.Request) bool {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	return len(h) >= 7 && strings.EqualFold(h[:7], "Bearer ")
}

func mutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func hasCookie(r *http.Request, name string) bool {
	c, err := r.Cookie(name)
	return err == nil && c != nil && c.Value != ""
}

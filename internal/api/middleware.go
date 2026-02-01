package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type contextKey string

const correlationIDKey contextKey = "correlation_id"

// CorrelationIDFromContext returns the request correlation ID.
func CorrelationIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(correlationIDKey).(string)
	return v
}

// CorrelationID extracts or generates X-Correlation-ID and injects into context.
func CorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Correlation-ID")
		if id == "" {
			id = uuid.New().String()
		}
		ctx := context.WithValue(r.Context(), correlationIDKey, id)
		w.Header().Set("X-Correlation-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logging logs request/response with correlation_id.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corr := CorrelationIDFromContext(r.Context())
		slog.Info("request",
			"correlation_id", corr,
			"method", r.Method,
			"path", r.URL.Path,
		)
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders adds security headers to all responses
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")
		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// XSS protection (legacy but still useful)
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		// HSTS - only set if using HTTPS
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		// Basic CSP - can be customized per application
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// CORS sets Access-Control-* headers. Use trusted origins in production.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	origins := make(map[string]bool)
	for _, o := range allowedOrigins {
		s := strings.TrimSpace(o)
		if s != "" {
			origins[s] = true
		}
	}
	// Dev fallback: always allow localhost / 127.0.0.1 when no origins configured
	allowLocalhost := len(origins) == 0
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := origins[origin] ||
				(allowLocalhost && origin != "" &&
					(strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:")))
			if allowed && origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Correlation-ID")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

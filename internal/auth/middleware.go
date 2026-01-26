package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

type contextKey string

const userIDContextKey contextKey = "user_id"

// UserIDFromContext returns the authenticated user_id from context, or "".
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDContextKey).(string)
	return v
}

// Middleware validates JWT via JWKS and injects user_id into context.
type Middleware struct {
	JWKS JWKSClient
}

// Wrap returns an http.Handler that rejects unauthenticated requests.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := extractBearer(r)
		if raw == "" {
			writeUnauthorized(w, "missing or invalid Authorization header")
			return
		}
		claims, err := m.JWKS.VerifyToken(r.Context(), raw)
		if err != nil {
			slog.Debug("jwt verification failed", "error", err)
			writeUnauthorized(w, "invalid or expired token")
			return
		}
		if claims.Sub == "" {
			writeUnauthorized(w, "token missing subject")
			return
		}
		ctx := context.WithValue(r.Context(), userIDContextKey, claims.Sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func extractBearer(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"` + escapeJSON(message) + `"}}`))
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

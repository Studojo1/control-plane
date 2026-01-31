package auth

import (
	"database/sql"
	"log/slog"
	"net/http"
)

// AdminMiddleware wraps auth middleware and checks if user has admin role.
type AdminMiddleware struct {
	JWKS JWKSClient
	DB   *sql.DB
}

// Wrap returns an http.Handler that rejects non-admin users.
func (m *AdminMiddleware) Wrap(next http.Handler) http.Handler {
	authMW := &Middleware{JWKS: m.JWKS}
	return authMW.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		if userID == "" {
			writeUnauthorized(w, "unauthorized")
			return
		}

		// Check if user has admin role
		var role sql.NullString
		err := m.DB.QueryRowContext(r.Context(), `
			SELECT role FROM "user" WHERE id = $1`,
			userID,
		).Scan(&role)
		if err != nil {
			if err == sql.ErrNoRows {
				writeForbidden(w, "user not found")
				return
			}
			slog.Error("failed to check user role", "error", err, "user_id", userID)
			writeInternalError(w, "failed to verify admin access")
			return
		}

		if !role.Valid || role.String != "admin" {
			writeForbidden(w, "admin access required")
			return
		}

		next.ServeHTTP(w, r)
	}))
}

func writeForbidden(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"` + escapeJSON(message) + `"}}`))
}

func writeInternalError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"` + escapeJSON(message) + `"}}`))
}


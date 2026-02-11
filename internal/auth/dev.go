package auth

import (
	"database/sql"
	"log/slog"
	"net/http"
)

// DevMiddleware wraps auth middleware and checks if user has dev role.
type DevMiddleware struct {
	JWKS JWKSClient
	DB   *sql.DB
}

// Wrap returns an http.Handler that rejects non-dev users.
func (m *DevMiddleware) Wrap(next http.Handler) http.Handler {
	authMW := &Middleware{JWKS: m.JWKS}
	return authMW.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		if userID == "" {
			writeUnauthorized(w, "unauthorized")
			return
		}

		// Check if user has dev role
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
			writeInternalError(w, "failed to verify dev access")
			return
		}

		if !role.Valid || (role.String != "dev" && role.String != "admin") {
			writeForbidden(w, "dev or admin access required")
			return
		}

		next.ServeHTTP(w, r)
	}))
}


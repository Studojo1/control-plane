package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/studojo/control-plane/internal/auth"
)

// EmailHandler handles email-related proxy requests to the emailer service.
type EmailHandler struct {
	EmailerServiceURL string
	HTTPClient        *http.Client
}

// NewEmailHandler creates a new EmailHandler.
func NewEmailHandler(emailerServiceURL string) *EmailHandler {
	if emailerServiceURL == "" {
		emailerServiceURL = "http://emailer-service:8087"
	}
	return &EmailHandler{
		EmailerServiceURL: emailerServiceURL,
		HTTPClient:        &http.Client{},
	}
}

// proxyRequest forwards a request to the emailer service and returns the response.
func (h *EmailHandler) proxyRequest(w http.ResponseWriter, r *http.Request, path string) {
	// Build target URL
	targetURL := strings.TrimSuffix(h.EmailerServiceURL, "/") + path

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read request body", "error", err)
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "failed to read request body")
		return
	}
	defer r.Body.Close()

	// Create new request to emailer service
	req, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		slog.Error("failed to create proxy request", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to create proxy request")
		return
	}

	// Copy headers (except Authorization - emailer service doesn't need it)
	for key, values := range r.Header {
		if strings.ToLower(key) != "authorization" {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}

	// Forward request
	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		slog.Error("failed to forward request to emailer service", "error", err, "path", path)
		WriteError(w, http.StatusBadGateway, ErrInternal, "email service unavailable")
		return
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("failed to read response body", "error", err)
		WriteError(w, http.StatusBadGateway, ErrInternal, "failed to read email service response")
		return
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write response status and body
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(respBody); err != nil {
		slog.Error("failed to write response", "error", err)
	}
}

// HandleForgotPassword handles POST /v1/email/forgot-password (public endpoint).
func (h *EmailHandler) HandleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, ErrValidationFailed, "method not allowed")
		return
	}
	h.proxyRequest(w, r, "/v1/email/forgot-password")
}

// HandleResetPassword handles POST /v1/email/reset-password (public endpoint).
func (h *EmailHandler) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, ErrValidationFailed, "method not allowed")
		return
	}
	h.proxyRequest(w, r, "/v1/email/reset-password")
}

// HandleChangePassword handles POST /v1/email/change-password (authenticated endpoint).
// Verifies that user_id in request body matches authenticated user.
func (h *EmailHandler) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, ErrValidationFailed, "method not allowed")
		return
	}

	// Verify authentication
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}

	// Read and parse request body to verify user_id
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "failed to read request body")
		return
	}
	r.Body.Close()

	// Parse JSON to check user_id
	var reqBody map[string]interface{}
	if err := json.Unmarshal(body, &reqBody); err == nil {
		if reqUserID, ok := reqBody["user_id"].(string); ok && reqUserID != userID {
			WriteError(w, http.StatusForbidden, ErrForbidden, "user_id does not match authenticated user")
			return
		}
		// Ensure user_id is set to authenticated user
		reqBody["user_id"] = userID
		// Re-marshal body
		body, _ = json.Marshal(reqBody)
	}

	// Create new request with verified body
	r.Body = io.NopCloser(bytes.NewReader(body))
	h.proxyRequest(w, r, "/v1/email/change-password")
}

// HandleGetEmailPreferences handles GET /v1/email/preferences/{user_id} (authenticated endpoint).
// Verifies that user_id in path matches authenticated user.
func (h *EmailHandler) HandleGetEmailPreferences(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, ErrValidationFailed, "method not allowed")
		return
	}

	// Verify authentication
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}

	// Verify user_id in path matches authenticated user
	pathUserID := r.PathValue("user_id")
	if pathUserID != userID {
		WriteError(w, http.StatusForbidden, ErrForbidden, "user_id does not match authenticated user")
		return
	}

	h.proxyRequest(w, r, "/v1/email/preferences/"+pathUserID)
}

// HandleUpdateEmailPreferences handles PUT /v1/email/preferences/{user_id} (authenticated endpoint).
// Verifies that user_id in path matches authenticated user.
func (h *EmailHandler) HandleUpdateEmailPreferences(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		WriteError(w, http.StatusMethodNotAllowed, ErrValidationFailed, "method not allowed")
		return
	}

	// Verify authentication
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}

	// Verify user_id in path matches authenticated user
	pathUserID := r.PathValue("user_id")
	if pathUserID != userID {
		WriteError(w, http.StatusForbidden, ErrForbidden, "user_id does not match authenticated user")
		return
	}

	h.proxyRequest(w, r, "/v1/email/preferences/"+pathUserID)
}


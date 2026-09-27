package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/studojo/control-plane/internal/auth"
)

// JobOutreachHandler proxies requests to the job-outreach-svc microservice.
type JobOutreachHandler struct {
	ServiceURL string
	HTTPClient *http.Client
}

// NewJobOutreachHandler creates a new JobOutreachHandler.
func NewJobOutreachHandler(serviceURL string) *JobOutreachHandler {
	if serviceURL == "" {
		serviceURL = "http://job-outreach-svc:8000"
	}
	return &JobOutreachHandler{
		ServiceURL: serviceURL,
		HTTPClient: &http.Client{},
	}
}

// forward proxies a request to job-outreach-svc, forwarding all headers and
// injecting X-User-Id from the validated JWT context so the backend can auth
// without relying on session cookies.
func (h *JobOutreachHandler) forward(w http.ResponseWriter, r *http.Request, path string) {
	targetURL := strings.TrimSuffix(h.ServiceURL, "/") + path

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("joboutreach: failed to read request body", "error", err)
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "failed to read request body")
		return
	}
	defer r.Body.Close()

	req, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		slog.Error("joboutreach: failed to create proxy request", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "proxy error")
		return
	}

	// Copy all headers (excluding Authorization — already validated at this layer)
	for key, values := range r.Header {
		if strings.ToLower(key) == "authorization" {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Inject authenticated user_id so job-outreach-svc can identify the caller
	if userID := auth.UserIDFromContext(r.Context()); userID != "" {
		req.Header.Set("X-User-Id", userID)
	}

	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		slog.Error("joboutreach: upstream request failed", "error", err, "path", path)
		WriteError(w, http.StatusBadGateway, ErrInternal, "upstream error")
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	respBody, _ := io.ReadAll(resp.Body)
	w.Write(respBody)
}

// ProxyAll is a catch-all handler that strips the /v1/outreach prefix and
// forwards everything to the job-outreach-svc at /api/v1/*.
func (h *JobOutreachHandler) ProxyAll(w http.ResponseWriter, r *http.Request) {
	// Request path: /v1/outreach/candidates/upload -> forward as /api/v1/candidates/upload
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/v1/outreach")
	if path == "" {
		path = "/"
	}
	targetPath := "/api/v1" + path

	// Preserve query string
	if r.URL.RawQuery != "" {
		targetPath += "?" + r.URL.RawQuery
	}

	h.forward(w, r, targetPath)
}

// HandleHealth proxies the health check.
func (h *JobOutreachHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, ErrValidationFailed, "method not allowed")
		return
	}
	h.forward(w, r, "/health")
}
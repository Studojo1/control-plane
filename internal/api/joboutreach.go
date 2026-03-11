package api

import (
	"net/http"
	"strings"
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

// proxyRequest forwards a request to job-outreach-svc and returns the response.
func (h *JobOutreachHandler) proxyRequest(w http.ResponseWriter, r *http.Request, path string) {
	proxy := &EmailHandler{
		EmailerServiceURL: h.ServiceURL,
		HTTPClient:        h.HTTPClient,
	}
	proxy.proxyRequest(w, r, path)
}

// proxyAll is a catch-all handler that strips the /v1/outreach prefix and
// forwards everything to the job-outreach-svc at /api/v1/*.
func (h *JobOutreachHandler) proxyAll(w http.ResponseWriter, r *http.Request) {
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

	h.proxyRequest(w, r, targetPath)
}

// HandleHealth proxies the health check.
func (h *JobOutreachHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, ErrValidationFailed, "method not allowed")
		return
	}
	h.proxyRequest(w, r, "/health")
}

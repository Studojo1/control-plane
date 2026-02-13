package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/studojo/control-plane/internal/auth"
	"github.com/studojo/control-plane/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
)

// DevHandler handles dev panel API requests
type DevHandler struct {
	DB             *sql.DB
	K8sClient      *k8s.Client
	GitHubClient   *GitHubClient
	AzureMonitor   *AzureMonitorClient
}

// ServiceStatus represents the status of a service
type ServiceStatus struct {
	Name           string    `json:"name"`
	Status         string    `json:"status"` // healthy, unhealthy, unknown
	Version        string    `json:"version"`
	Replicas       int       `json:"replicas"`
	ReadyReplicas  int       `json:"ready_replicas"`
	LastDeployment time.Time `json:"last_deployment"`
}

// DeploymentHistory represents a deployment record
type DeploymentHistory struct {
	Service     string    `json:"service"`
	Version     string    `json:"version"`
	DeployedAt  time.Time `json:"deployed_at"`
	DeployedBy  string    `json:"deployed_by"`
	Status      string    `json:"status"`
	WorkflowRun int64     `json:"workflow_run,omitempty"`
}

// HandleListServices returns the status of all services from Kubernetes
func (h *DevHandler) HandleListServices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// If K8s client is not available, return placeholder data
	if h.K8sClient == nil {
		slog.Warn("K8s client not available, returning placeholder data")
		services := []ServiceStatus{
			{Name: "frontend", Status: "unknown", Version: "unknown", Replicas: 0, ReadyReplicas: 0, LastDeployment: time.Now()},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"services": services,
			"timestamp": time.Now(),
		})
		return
	}

	deployments, err := h.K8sClient.GetDeployments(ctx)
	if err != nil {
		slog.Error("failed to get deployments", "error", err)
		http.Error(w, "failed to query services", http.StatusInternalServerError)
		return
	}

	var services []ServiceStatus
	for _, deployment := range deployments {
		// Skip system deployments
		if strings.HasPrefix(deployment.Name, "kube-") || strings.HasPrefix(deployment.Name, "azure-") {
			continue
		}

		status := determineStatus(deployment)
		version := deployment.Labels["version"]
		if version == "" {
			version = "unknown"
		}

		lastDeployment := deployment.CreationTimestamp.Time
		if deployment.Status.UpdatedReplicas > 0 {
			// Try to get more accurate timestamp from conditions
			for _, condition := range deployment.Status.Conditions {
				if condition.Type == "Progressing" && condition.Status == "True" {
					lastDeployment = condition.LastUpdateTime.Time
					break
				}
			}
		}

		replicas := int32(1)
		if deployment.Spec.Replicas != nil {
			replicas = *deployment.Spec.Replicas
		}

		services = append(services, ServiceStatus{
			Name:           deployment.Name,
			Status:         status,
			Version:        version,
			Replicas:       int(replicas),
			ReadyReplicas:  int(deployment.Status.ReadyReplicas),
			LastDeployment: lastDeployment,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"services": services,
		"timestamp": time.Now(),
	})
}

// determineStatus determines service health status from deployment
func determineStatus(deployment appsv1.Deployment) string {
	replicas := int32(1)
	if deployment.Spec.Replicas != nil {
		replicas = *deployment.Spec.Replicas
	}
	readyReplicas := int(deployment.Status.ReadyReplicas)
	availableReplicas := int(deployment.Status.AvailableReplicas)

	if readyReplicas == int(replicas) && availableReplicas == int(replicas) {
		return "healthy"
	}
	if readyReplicas > 0 {
		return "degraded"
	}
	return "unhealthy"
}

// HandleQueryLogs proxies log queries to Azure Monitor Log Analytics
func (h *DevHandler) HandleQueryLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("query")
	service := r.URL.Query().Get("service")
	limit := r.URL.Query().Get("limit")
	
	if limit == "" {
		limit = "100"
	}

	var logs []map[string]interface{}
	var err error

	// Build KQL query if service is specified
	kqlQuery := query
	if service != "" && query == "" {
		kqlQuery = fmt.Sprintf("ContainerLog | where ServiceName == '%s' | order by TimeGenerated desc | take %s", service, limit)
	} else if query == "" {
		kqlQuery = fmt.Sprintf("ContainerLog | order by TimeGenerated desc | take %s", limit)
	}

	if h.AzureMonitor != nil {
		logs, err = h.AzureMonitor.QueryLogs(ctx, kqlQuery, 1*time.Hour)
		if err != nil {
			slog.Warn("Azure Monitor query failed, falling back to placeholder", "error", err)
		}
	}

	// Fallback to placeholder if Azure Monitor is not available or query failed
	if len(logs) == 0 {
		logs = []map[string]interface{}{
			{
				"timestamp": time.Now().Add(-5 * time.Minute),
				"service":   service,
				"level":     "info",
				"message":   "Sample log entry (Azure Monitor not configured)",
				"query":     query,
			},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs":      logs,
		"query":     query,
		"service":   service,
		"limit":     limit,
		"timestamp": time.Now(),
	})
}

// HandleQueryMetrics proxies metric queries to Azure Monitor
func (h *DevHandler) HandleQueryMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	service := r.URL.Query().Get("service")
	metric := r.URL.Query().Get("metric")
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	startTime := time.Now().Add(-1 * time.Hour)
	endTime := time.Now()

	if startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = t
		}
	}
	if endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = t
		}
	}

	var dataPoints []MetricDataPoint
	var err error

	// Build resource URI for the service
	resourceURI := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/studojo",
		os.Getenv("AZURE_SUBSCRIPTION_ID"),
		os.Getenv("AZURE_RESOURCE_GROUP"))

	if h.AzureMonitor != nil && metric != "" {
		dataPoints, err = h.AzureMonitor.QueryMetrics(ctx, resourceURI, metric, startTime, endTime)
		if err != nil {
			slog.Warn("Azure Monitor metrics query failed, falling back to placeholder", "error", err)
		}
	}

	// Fallback to placeholder if Azure Monitor is not available or query failed
	if len(dataPoints) == 0 {
		dataPoints = []MetricDataPoint{
			{Timestamp: startTime, Value: 0.5},
			{Timestamp: endTime, Value: 0.7},
		}
	}

	// Convert to response format
	values := make([]map[string]interface{}, len(dataPoints))
	for i, dp := range dataPoints {
		values[i] = map[string]interface{}{
			"timestamp": dp.Timestamp.Format(time.RFC3339),
			"value":     dp.Value,
		}
	}

	metrics := map[string]interface{}{
		"service": service,
		"metric":  metric,
		"values":  values,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// HandleGetCICDStatus returns CI/CD pipeline status from GitHub Actions
func (h *DevHandler) HandleGetCICDStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	service := r.URL.Query().Get("service")
	
	var workflows []WorkflowRun
	var err error
	
	if h.GitHubClient != nil {
		workflowName := "Deploy to Kubernetes"
		if service != "" {
			workflowName = fmt.Sprintf("%s deployment", service)
		}
		workflows, err = h.GitHubClient.GetWorkflowRuns(ctx, workflowName, 10)
		if err != nil {
			slog.Warn("failed to get GitHub workflow runs", "error", err, "service", service)
			// Fall back to empty list
			workflows = []WorkflowRun{}
		}
	} else {
		// No GitHub client, return empty
		workflows = []WorkflowRun{}
	}

	// Convert to response format
	workflowList := make([]map[string]interface{}, len(workflows))
	for i, wf := range workflows {
		workflowList[i] = map[string]interface{}{
			"name":       wf.Name,
			"status":     wf.Status,
			"conclusion": wf.Conclusion,
			"run_id":     wf.ID,
			"created_at": wf.CreatedAt,
			"updated_at": wf.UpdatedAt,
			"html_url":   wf.HTMLURL,
		}
	}

	status := map[string]interface{}{
		"service":  service,
		"workflows": workflowList,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// HandleGetDeployments returns deployment history
func (h *DevHandler) HandleGetDeployments(w http.ResponseWriter, r *http.Request) {
	// Query deployment history from database
	// For now, we'll create a table to store this
	// In production, this would query a deployments table
	
	service := r.URL.Query().Get("service")
	
	var deployments []DeploymentHistory
	
	if service != "" {
		// Query specific service
		rows, err := h.DB.Query(`
			SELECT service, version, deployed_at, deployed_by, status, workflow_run
			FROM cp.deployment_history
			WHERE service = $1
			ORDER BY deployed_at DESC
			LIMIT 50
		`, service)
		if err != nil {
			// Table might not exist yet, return empty
			slog.Warn("deployment history query failed", "error", err)
			deployments = []DeploymentHistory{}
		} else {
			defer rows.Close()
			for rows.Next() {
				var d DeploymentHistory
				var workflowRun sql.NullInt64
				if err := rows.Scan(&d.Service, &d.Version, &d.DeployedAt, &d.DeployedBy, &d.Status, &workflowRun); err == nil {
					if workflowRun.Valid {
						d.WorkflowRun = workflowRun.Int64
					}
					deployments = append(deployments, d)
				}
			}
		}
	} else {
		// Query all services
		rows, err := h.DB.Query(`
			SELECT service, version, deployed_at, deployed_by, status, workflow_run
			FROM cp.deployment_history
			ORDER BY deployed_at DESC
			LIMIT 100
		`)
		if err != nil {
			slog.Warn("deployment history query failed", "error", err)
			deployments = []DeploymentHistory{}
		} else {
			defer rows.Close()
			for rows.Next() {
				var d DeploymentHistory
				var workflowRun sql.NullInt64
				if err := rows.Scan(&d.Service, &d.Version, &d.DeployedAt, &d.DeployedBy, &d.Status, &workflowRun); err == nil {
					if workflowRun.Valid {
						d.WorkflowRun = workflowRun.Int64
					}
					deployments = append(deployments, d)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"deployments": deployments,
		"count":       len(deployments),
	})
}

// HandleRecordDeployment records a deployment in the database
func (h *DevHandler) HandleRecordDeployment(w http.ResponseWriter, r *http.Request) {
	var deployment DeploymentHistory
	if err := json.NewDecoder(r.Body).Decode(&deployment); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Ensure deployment_history table exists
	_, err := h.DB.Exec(`
		CREATE TABLE IF NOT EXISTS cp.deployment_history (
			id SERIAL PRIMARY KEY,
			service TEXT NOT NULL,
			version TEXT NOT NULL,
			deployed_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deployed_by TEXT,
			status TEXT NOT NULL,
			workflow_run BIGINT,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		slog.Error("failed to create deployment_history table", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Insert deployment record
	_, err = h.DB.Exec(`
		INSERT INTO cp.deployment_history (service, version, deployed_at, deployed_by, status, workflow_run)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, deployment.Service, deployment.Version, deployment.DeployedAt, deployment.DeployedBy, deployment.Status, deployment.WorkflowRun)
	if err != nil {
		slog.Error("failed to insert deployment", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
}

// HandleGetDeploymentHistory returns deployment version history from Kubernetes ReplicaSets
func (h *DevHandler) HandleGetDeploymentHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	service := r.PathValue("service")
	if service == "" {
		http.Error(w, "service name required", http.StatusBadRequest)
		return
	}

	if h.K8sClient == nil {
		http.Error(w, "K8s client not available", http.StatusServiceUnavailable)
		return
	}

	replicaSets, err := h.K8sClient.GetReplicaSets(ctx, service)
	if err != nil {
		slog.Error("failed to get replica sets", "service", service, "error", err)
		http.Error(w, "failed to get deployment history", http.StatusInternalServerError)
		return
	}

	var history []k8s.DeploymentVersion
	for _, rs := range replicaSets {
		version := rs.Labels["version"]
		if version == "" {
			version = "unknown"
		}

		replicas := int32(1)
		if rs.Spec.Replicas != nil {
			replicas = *rs.Spec.Replicas
		}

		history = append(history, k8s.DeploymentVersion{
			Version:        version,
			ReplicaSetName: rs.Name,
			Replicas:       int(replicas),
			ReadyReplicas:  int(rs.Status.ReadyReplicas),
			CreatedAt:      rs.CreationTimestamp.Time,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"history": history,
		"count":   len(history),
	})
}

// HandleRollbackDeployment rolls back a deployment to a previous version
func (h *DevHandler) HandleRollbackDeployment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	service := r.PathValue("service")
	if service == "" {
		http.Error(w, "service name required", http.StatusBadRequest)
		return
	}

	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Version == "" {
		http.Error(w, "version required", http.StatusBadRequest)
		return
	}

	if h.K8sClient == nil {
		http.Error(w, "K8s client not available", http.StatusServiceUnavailable)
		return
	}

	// Get all replica sets to find the one with matching version
	replicaSets, err := h.K8sClient.GetReplicaSets(ctx, service)
	if err != nil {
		slog.Error("failed to get replica sets", "service", service, "error", err)
		http.Error(w, "failed to get deployment history", http.StatusInternalServerError)
		return
	}

	// Find ReplicaSet with matching version
	var targetRS *appsv1.ReplicaSet
	for i := range replicaSets {
		if replicaSets[i].Labels["version"] == req.Version {
			targetRS = &replicaSets[i]
			break
		}
	}

	if targetRS == nil {
		http.Error(w, fmt.Sprintf("version %s not found", req.Version), http.StatusNotFound)
		return
	}

	// Perform rollback
	if err := h.K8sClient.RollbackDeployment(ctx, service, targetRS.Name); err != nil {
		slog.Error("failed to rollback deployment", "service", service, "version", req.Version, "error", err)
		http.Error(w, "failed to rollback deployment", http.StatusInternalServerError)
		return
	}

	// Record rollback in database
	userID := auth.UserIDFromContext(ctx)
	_, err = h.DB.Exec(`
		INSERT INTO cp.deployment_history (service, version, deployed_at, deployed_by, status, workflow_run)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, service, req.Version, time.Now(), userID, "rollback", nil)
	if err != nil {
		slog.Warn("failed to record rollback in database", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "rolled back", "version": req.Version})
}

// HandleGetTelemetry returns developer telemetry data
func (h *DevHandler) HandleGetTelemetry(w http.ResponseWriter, r *http.Request) {
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	startTime := time.Now().Add(-24 * time.Hour)
	endTime := time.Now()

	if startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = t
		}
	}
	if endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = t
		}
	}

	// Query telemetry from database
	rows, err := h.DB.Query(`
		SELECT 
			service,
			COUNT(*) as api_calls,
			COUNT(DISTINCT user_id) as unique_users,
			AVG(response_time_ms) as avg_response_time,
			COUNT(CASE WHEN status_code >= 400 THEN 1 END) as error_count
		FROM cp.telemetry
		WHERE created_at BETWEEN $1 AND $2
		GROUP BY service
		ORDER BY api_calls DESC
	`, startTime, endTime)
	
	telemetry := []map[string]interface{}{}
	if err != nil {
		// Table might not exist yet
		slog.Warn("telemetry query failed", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var service string
			var apiCalls, uniqueUsers, errorCount int
			var avgResponseTime sql.NullFloat64
			
			if err := rows.Scan(&service, &apiCalls, &uniqueUsers, &avgResponseTime, &errorCount); err == nil {
				telemetry = append(telemetry, map[string]interface{}{
					"service":           service,
					"api_calls":         apiCalls,
					"unique_users":      uniqueUsers,
					"avg_response_time": avgResponseTime.Float64,
					"error_count":       errorCount,
					"error_rate":        float64(errorCount) / float64(apiCalls) * 100,
				})
			}
		}
	}

	// Ensure telemetry table exists
	_, _ = h.DB.Exec(`
		CREATE TABLE IF NOT EXISTS cp.telemetry (
			id SERIAL PRIMARY KEY,
			service TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			user_id TEXT,
			status_code INTEGER,
			response_time_ms INTEGER,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"telemetry": telemetry,
		"start":     startTime,
		"end":       endTime,
		"count":     len(telemetry),
	})
}

// HandleRecordTelemetry records a telemetry event
func (h *DevHandler) HandleRecordTelemetry(w http.ResponseWriter, r *http.Request) {
	var event struct {
		Service        string `json:"service"`
		Endpoint       string `json:"endpoint"`
		UserID         string `json:"user_id"`
		StatusCode     int    `json:"status_code"`
		ResponseTimeMs int    `json:"response_time_ms"`
	}

	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Ensure telemetry table exists
	_, err := h.DB.Exec(`
		CREATE TABLE IF NOT EXISTS cp.telemetry (
			id SERIAL PRIMARY KEY,
			service TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			user_id TEXT,
			status_code INTEGER,
			response_time_ms INTEGER,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		slog.Error("failed to create telemetry table", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Insert telemetry record
	_, err = h.DB.Exec(`
		INSERT INTO cp.telemetry (service, endpoint, user_id, status_code, response_time_ms)
		VALUES ($1, $2, $3, $4, $5)
	`, event.Service, event.Endpoint, event.UserID, event.StatusCode, event.ResponseTimeMs)
	if err != nil {
		slog.Error("failed to insert telemetry", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
}


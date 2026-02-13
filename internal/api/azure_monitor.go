package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"
)

// AzureMonitorClient wraps Azure Monitor API client
type AzureMonitorClient struct {
	subscriptionID string
	resourceGroup  string
	workspaceID    string
	client         *http.Client
}

// NewAzureMonitorClient creates a new Azure Monitor client
func NewAzureMonitorClient() *AzureMonitorClient {
	return &AzureMonitorClient{
		subscriptionID: os.Getenv("AZURE_SUBSCRIPTION_ID"),
		resourceGroup:  os.Getenv("AZURE_RESOURCE_GROUP"),
		workspaceID:    os.Getenv("AZURE_LOG_ANALYTICS_WORKSPACE_ID"),
		client:         &http.Client{Timeout: 30 * time.Second},
	}
}

// QueryLogs queries Azure Monitor Log Analytics
func (a *AzureMonitorClient) QueryLogs(ctx context.Context, query string, timespan time.Duration) ([]map[string]interface{}, error) {
	if a.workspaceID == "" {
		slog.Warn("Azure Log Analytics workspace ID not set, returning empty logs")
		return []map[string]interface{}{}, nil
	}

	// In production, this would use Azure Monitor REST API
	// For now, return placeholder
	// The actual implementation would:
	// 1. Get Azure AD token
	// 2. Call Azure Monitor Log Analytics API
	// 3. Parse and return results

	return []map[string]interface{}{
		{
			"timestamp": time.Now().Add(-5 * time.Minute),
			"level":     "info",
			"message":   "Sample log entry from Azure Monitor",
			"query":     query,
		},
	}, nil
}

// QueryMetrics queries Azure Monitor Metrics
func (a *AzureMonitorClient) QueryMetrics(ctx context.Context, resourceURI, metricName string, startTime, endTime time.Time) ([]MetricDataPoint, error) {
	if a.subscriptionID == "" || a.resourceGroup == "" {
		slog.Warn("Azure subscription or resource group not set, returning empty metrics")
		return []MetricDataPoint{}, nil
	}

	// In production, this would use Azure Monitor Metrics API
	// For now, return placeholder
	// The actual implementation would:
	// 1. Get Azure AD token
	// 2. Call Azure Monitor Metrics API
	// 3. Parse and return results

	return []MetricDataPoint{
		{Timestamp: startTime, Value: 0.5},
		{Timestamp: endTime, Value: 0.7},
	}, nil
}

// MetricDataPoint represents a single metric data point
type MetricDataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// GetAccessToken gets Azure AD access token for API calls
func (a *AzureMonitorClient) GetAccessToken(ctx context.Context) (string, error) {
	// In production, use managed identity or service principal
	// For now, return empty (will use placeholder data)
	token := os.Getenv("AZURE_ACCESS_TOKEN")
	if token == "" {
		return "", fmt.Errorf("AZURE_ACCESS_TOKEN not set")
	}
	return token, nil
}

// buildLogAnalyticsURL builds the URL for Log Analytics API
func (a *AzureMonitorClient) buildLogAnalyticsURL() string {
	if a.workspaceID == "" {
		return ""
	}
	return fmt.Sprintf("https://api.loganalytics.io/v1/workspaces/%s/query", a.workspaceID)
}

// buildMetricsURL builds the URL for Metrics API
func (a *AzureMonitorClient) buildMetricsURL(resourceURI string) string {
	encodedURI := url.QueryEscape(resourceURI)
	return fmt.Sprintf("https://management.azure.com%s/providers/Microsoft.Insights/metrics", encodedURI)
}

// executeQuery executes a query against Azure Monitor API
func (a *AzureMonitorClient) executeQuery(ctx context.Context, apiURL string, body io.Reader) ([]byte, error) {
	token, err := a.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}


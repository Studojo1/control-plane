package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/go-github/v65/github"
	"golang.org/x/oauth2"
)

// GitHubClient wraps GitHub API client
type GitHubClient struct {
	client *github.Client
	owner  string
	repo   string
}

// NewGitHubClient creates a new GitHub API client
func NewGitHubClient() *GitHubClient {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		slog.Warn("GITHUB_TOKEN not set, GitHub integration will be limited")
		return &GitHubClient{
			client: github.NewClient(nil),
			owner:  "studojo",
			repo:   "studojo",
		}
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	return &GitHubClient{
		client: github.NewClient(tc),
		owner:  "studojo",
		repo:   "studojo",
	}
}

// WorkflowRun represents a GitHub Actions workflow run
type WorkflowRun struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Conclusion string   `json:"conclusion"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	HTMLURL   string    `json:"html_url"`
}

// GetWorkflowRuns gets workflow runs for the repository
func (g *GitHubClient) GetWorkflowRuns(ctx context.Context, workflowName string, limit int) ([]WorkflowRun, error) {
	if g.client == nil {
		return []WorkflowRun{}, nil
	}

	opts := &github.ListWorkflowRunsOptions{
		ListOptions: github.ListOptions{
			PerPage: limit,
		},
	}

	// If workflow name is provided, try to find it
	var workflowID *int64
	if workflowName != "" {
		workflows, _, err := g.client.Actions.ListWorkflows(ctx, g.owner, g.repo, nil)
		if err == nil {
			for _, wf := range workflows.Workflows {
				if *wf.Name == workflowName || *wf.Name == fmt.Sprintf("%s CI/CD", workflowName) {
					workflowID = wf.ID
					break
				}
			}
		}
	}

	// Use workflow-specific endpoint if workflow ID is found
	var runs *github.WorkflowRuns
	var resp *github.Response
	var err error
	if workflowID != nil {
		runs, resp, err = g.client.Actions.ListWorkflowRunsByID(ctx, g.owner, g.repo, *workflowID, opts)
	} else {
		runs, resp, err = g.client.Actions.ListRepositoryWorkflowRuns(ctx, g.owner, g.repo, opts)
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			// Repository might not be accessible, return empty
			return []WorkflowRun{}, nil
		}
		return nil, fmt.Errorf("failed to list workflow runs: %w", err)
	}

	var result []WorkflowRun
	for _, run := range runs.WorkflowRuns {
		status := "unknown"
		if run.Status != nil {
			status = *run.Status
		}

		conclusion := ""
		if run.Conclusion != nil {
			conclusion = *run.Conclusion
		}

		name := "Unknown"
		if run.Name != nil {
			name = *run.Name
		}

		htmlURL := ""
		if run.HTMLURL != nil {
			htmlURL = *run.HTMLURL
		}

		result = append(result, WorkflowRun{
			ID:         *run.ID,
			Name:       name,
			Status:     status,
			Conclusion: conclusion,
			CreatedAt:  run.CreatedAt.Time,
			UpdatedAt:  run.UpdatedAt.Time,
			HTMLURL:    htmlURL,
		})
	}

	return result, nil
}

// GetWorkflowRunStatus gets the status of a specific workflow run
func (g *GitHubClient) GetWorkflowRunStatus(ctx context.Context, runID int64) (*WorkflowRun, error) {
	if g.client == nil {
		return nil, fmt.Errorf("GitHub client not initialized")
	}

	run, _, err := g.client.Actions.GetWorkflowRunByID(ctx, g.owner, g.repo, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow run: %w", err)
	}

	status := "unknown"
	if run.Status != nil {
		status = *run.Status
	}

	conclusion := ""
	if run.Conclusion != nil {
		conclusion = *run.Conclusion
	}

	name := "Unknown"
	if run.Name != nil {
		name = *run.Name
	}

	htmlURL := ""
	if run.HTMLURL != nil {
		htmlURL = *run.HTMLURL
	}

	return &WorkflowRun{
		ID:         *run.ID,
		Name:       name,
		Status:     status,
		Conclusion: conclusion,
		CreatedAt:  run.CreatedAt.Time,
		UpdatedAt:  run.UpdatedAt.Time,
		HTMLURL:    htmlURL,
	}, nil
}


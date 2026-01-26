package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/studojo/control-plane/internal/messaging"
	"github.com/studojo/control-plane/internal/store"
)

// Service orchestrates job submission, state transitions, and status.
type Service struct {
	Store     store.JobStore
	Publisher messaging.Publisher
}

// SubmitJob creates job, checks idempotency, enqueues, returns job_id.
func (s *Service) SubmitJob(ctx context.Context, req *SubmitJobRequest) (*SubmitJobResponse, error) {
	if req.Type == "" || len(req.Payload) == 0 {
		return nil, ErrValidation
	}
	// Supported types for routing
	if req.Type != "assignment-gen" && req.Type != "outline-gen" && req.Type != "outline-edit" && req.Type != "resume-gen" && req.Type != "resume-optimize" {
		return nil, ErrValidation
	}

	// Idempotency: same key, same user -> return existing job; same key, different user -> 409.
	if req.IdempotencyKey != "" {
		existingID, existingUser, ok, err := s.Store.GetIdempotencyKeyByKey(ctx, req.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if ok {
			if existingUser != req.UserID {
				return nil, ErrConflict
			}
			job, err := s.Store.GetJob(ctx, existingID)
			if err != nil || job == nil {
				return nil, err
			}
			out := &SubmitJobResponse{
				JobID:     job.ID.String(),
				Status:    job.Status,
				CreatedAt: job.CreatedAt.Format(time.RFC3339),
				IsReplay:  true,
			}
			if len(job.Result) > 0 {
				var r any
				_ = json.Unmarshal(job.Result, &r)
				out.Result = r
			}
			return out, nil
		}
	}

	now := time.Now().UTC()
	jobID := uuid.New()
	j := &store.Job{
		ID:        jobID,
		UserID:    req.UserID,
		Type:      req.Type,
		Status:    "CREATED",
		Payload:   req.Payload,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.Store.CreateJob(ctx, j); err != nil {
		return nil, err
	}

	if req.IdempotencyKey != "" {
		keyID, err := s.Store.CreateIdempotencyKey(ctx, req.IdempotencyKey, req.UserID, jobID, 24*time.Hour)
		if err != nil {
			return nil, err
		}
		if err := s.Store.UpdateJobIdempotencyKey(ctx, jobID, keyID); err != nil {
			return nil, err
		}
	}

	cmd := &messaging.JobCommand{
		JobID:         jobID.String(),
		Type:          req.Type,
		UserID:        req.UserID,
		Payload:       req.Payload,
		CorrelationID: uuid.New().String(),
	}
	if err := s.Publisher.PublishJob(ctx, cmd); err != nil {
		return nil, fmt.Errorf("publish job: %w", err)
	}

	if err := s.Store.RecordTransition(ctx, jobID, "CREATED", "QUEUED", nil); err != nil {
		slog.Warn("record transition CREATED->QUEUED failed", "job_id", jobID, "error", err)
	}
	if err := s.Store.UpdateJobStatus(ctx, jobID, "QUEUED", nil, nil); err != nil {
		slog.Warn("update job status QUEUED failed", "job_id", jobID, "error", err)
	}

	return &SubmitJobResponse{
		JobID:     jobID.String(),
		Status:    "QUEUED",
		CreatedAt: now.Format(time.RFC3339),
		IsReplay:  false,
	}, nil
}

// GetJob returns job status and optional result/error.
func (s *Service) GetJob(ctx context.Context, jobIDStr, userID string) (*JobResponse, error) {
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		return nil, ErrNotFound
	}
	job, err := s.Store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNotFound
	}
	if job.UserID != userID {
		return nil, ErrForbidden
	}
	return jobToResponse(job), nil
}

// ListJobs returns jobs for a user, optionally filtered by type, with pagination.
func (s *Service) ListJobs(ctx context.Context, userID, jobType string, limit, offset int) ([]JobResponse, error) {
	// Default limit if not specified
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100 // Cap at 100
	}
	if offset < 0 {
		offset = 0
	}
	
	jobs, err := s.Store.ListJobs(ctx, userID, jobType, limit, offset)
	if err != nil {
		return nil, err
	}
	
	// Convert to JobResponse format
	responses := make([]JobResponse, 0, len(jobs))
	for _, job := range jobs {
		responses = append(responses, *jobToResponse(job))
	}
	
	return responses, nil
}

// HandleResult processes result events from workers; called by messaging consumer.
func (s *Service) HandleResult(ctx context.Context, event *messaging.ResultEvent) error {
	jobID, err := uuid.Parse(event.JobID)
	if err != nil {
		return err
	}
	job, err := s.Store.GetJob(ctx, jobID)
	if err != nil || job == nil {
		return err
	}
	if !CanTransition(job.Status, event.Status) {
		slog.Debug("ignore result: invalid transition", "job_id", event.JobID, "from", job.Status, "to", event.Status)
		return nil
	}
	var resultBytes []byte
	if len(event.Result) > 0 {
		resultBytes = []byte(event.Result)
	}
	if err := s.Store.UpdateJobStatus(ctx, jobID, event.Status, resultBytes, event.Error); err != nil {
		return err
	}
	return s.Store.RecordTransition(ctx, jobID, job.Status, event.Status, map[string]any{
		"correlation_id": event.CorrelationID,
	})
}

func jobToResponse(j *store.Job) *JobResponse {
	res := &JobResponse{
		JobID:     j.ID.String(),
		Status:    j.Status,
		CreatedAt: j.CreatedAt.Format(time.RFC3339),
		UpdatedAt: j.UpdatedAt.Format(time.RFC3339),
		Error:     j.Error,
	}
	if len(j.Result) > 0 {
		var r any
		_ = json.Unmarshal(j.Result, &r)
		res.Result = r
	}
	return res
}

// SubmitJobRequest input for job submission.
type SubmitJobRequest struct {
	UserID         string
	IdempotencyKey string
	Type           string
	Payload        []byte
}

// SubmitJobResponse returned on submit. IsReplay true => return 200 with existing job.
type SubmitJobResponse struct {
	JobID     string
	Status    string
	CreatedAt string
	Result    any
	IsReplay  bool
}

// JobResponse returned for GET /jobs/:id.
type JobResponse struct {
	JobID     string
	Status    string
	CreatedAt string
	UpdatedAt string
	Result    any
	Error     *string
}

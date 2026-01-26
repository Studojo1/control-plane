package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// JobStore persistence for jobs, transitions, idempotency keys.
type JobStore interface {
	CreateJob(ctx context.Context, j *Job) error
	GetJob(ctx context.Context, id uuid.UUID) (*Job, error)
	UpdateJobStatus(ctx context.Context, id uuid.UUID, status string, result []byte, err *string) error
	UpdateJobIdempotencyKey(ctx context.Context, jobID, keyID uuid.UUID) error
	RecordTransition(ctx context.Context, jobID uuid.UUID, from, to string, metadata map[string]any) error

	CreateIdempotencyKey(ctx context.Context, key, userID string, jobID uuid.UUID, ttl time.Duration) (keyID uuid.UUID, err error)
	GetIdempotencyKey(ctx context.Context, key, userID string) (jobID uuid.UUID, ok bool, err error)
	// GetIdempotencyKeyByKey returns job_id and user_id for key; used to detect same-key-different-user (409).
	GetIdempotencyKeyByKey(ctx context.Context, key string) (jobID uuid.UUID, userID string, ok bool, err error)
	
	// ListJobs returns jobs for a user, optionally filtered by type, with pagination.
	ListJobs(ctx context.Context, userID string, jobType string, limit, offset int) ([]*Job, error)
}

// Job domain model.
type Job struct {
	ID               uuid.UUID
	UserID           string
	Type             string
	Status           string
	IdempotencyKeyID *uuid.UUID
	Payload          json.RawMessage
	Result           json.RawMessage
	Error            *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

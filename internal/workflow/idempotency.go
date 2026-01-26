package workflow

import "context"

// IdempotencyChecker checks/creates idempotency keys.
type IdempotencyChecker interface {
	GetByKey(ctx context.Context, key, userID string) (jobID string, ok bool, err error)
	Create(ctx context.Context, key, userID, jobID string) error
}

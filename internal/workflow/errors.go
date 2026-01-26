package workflow

import "errors"

var (
	ErrNotFound   = errors.New("job not found")
	ErrForbidden  = errors.New("job belongs to another user")
	ErrConflict   = errors.New("idempotency key already used by another user")
	ErrValidation = errors.New("validation failed")
)

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PostgresStore implements JobStore using PostgreSQL (schema cp).
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore returns a PostgresStore.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// CreateJob implements JobStore.
func (s *PostgresStore) CreateJob(ctx context.Context, j *Job) error {
	var keyID interface{}
	if j.IdempotencyKeyID != nil {
		keyID = j.IdempotencyKeyID.String()
	}
	var resultVal, errVal interface{}
	if len(j.Result) > 0 {
		resultVal = j.Result
	}
	if j.Error != nil {
		errVal = *j.Error
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cp.jobs (id, user_id, type, status, idempotency_key_id, payload, result, error, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		j.ID.String(), j.UserID, j.Type, j.Status, keyID, j.Payload, resultVal, errVal, j.CreatedAt, j.UpdatedAt,
	)
	return err
}

// GetJob implements JobStore.
func (s *PostgresStore) GetJob(ctx context.Context, id uuid.UUID) (*Job, error) {
	var j Job
	var idStr string
	var payload, result []byte
	var errMsg sql.NullString
	var keyID sql.NullString
	var progress sql.NullInt64
	var currentSection sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, type, status, idempotency_key_id, payload, result, error, progress, current_section, created_at, updated_at
		FROM cp.jobs WHERE id = $1`,
		id.String(),
	).Scan(&idStr, &j.UserID, &j.Type, &j.Status, &keyID, &payload, &result, &errMsg, &progress, &currentSection, &j.CreatedAt, &j.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.ID, _ = uuid.Parse(idStr)
	j.Payload = payload
	j.Result = result
	if errMsg.Valid {
		j.Error = &errMsg.String
	}
	if keyID.Valid {
		u, _ := uuid.Parse(keyID.String)
		j.IdempotencyKeyID = &u
	}
	if progress.Valid {
		p := int(progress.Int64)
		j.Progress = &p
	}
	if currentSection.Valid {
		j.CurrentSection = &currentSection.String
	}
	return &j, nil
}

// UpdateJobStatus implements JobStore.
func (s *PostgresStore) UpdateJobStatus(ctx context.Context, id uuid.UUID, status string, result []byte, errMsg *string) error {
	var resultVal, errVal interface{}
	if len(result) > 0 {
		resultVal = result
	}
	if errMsg != nil {
		errVal = *errMsg
	}
	// Allow updates from any non-terminal state to terminal state (COMPLETED/FAILED)
	// This handles cases where the job might be in QUEUED or RUNNING when result arrives
	updateResult, err := s.db.ExecContext(ctx, `
		UPDATE cp.jobs SET status = $1, result = $2, error = $3, updated_at = $4
		WHERE id = $5 AND status IN ('CREATED', 'RUNNING', 'QUEUED')`,
		status, resultVal, errVal, time.Now().UTC(), id.String(),
	)
	if err != nil {
		return err
	}
	rowsAffected, _ := updateResult.RowsAffected()
	if rowsAffected == 0 && (status == "COMPLETED" || status == "FAILED") {
		// If no rows were affected and we're trying to set a terminal state,
		// check if job is already in that state and just update result/error if needed
		var currentStatus string
		err := s.db.QueryRowContext(ctx, `SELECT status FROM cp.jobs WHERE id = $1`, id.String()).Scan(&currentStatus)
		if err == nil && currentStatus == status {
			// Job is already in the correct terminal state, just update result/error
			_, err = s.db.ExecContext(ctx, `
				UPDATE cp.jobs SET result = $1, error = $2, updated_at = $3
				WHERE id = $4 AND status = $5`,
				resultVal, errVal, time.Now().UTC(), id.String(), status,
			)
			return err
		}
	}
	return nil
	return err
}

// UpdateJobProgress implements JobStore.
func (s *PostgresStore) UpdateJobProgress(ctx context.Context, id uuid.UUID, progress int, currentSection string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cp.jobs SET progress = $1, current_section = $2, updated_at = $3
		WHERE id = $4`,
		progress, currentSection, time.Now().UTC(), id.String(),
	)
	return err
}

// UpdateJobIdempotencyKey implements JobStore.
func (s *PostgresStore) UpdateJobIdempotencyKey(ctx context.Context, jobID, keyID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cp.jobs SET idempotency_key_id = $1, updated_at = $2 WHERE id = $3`,
		keyID.String(), time.Now().UTC(), jobID.String(),
	)
	return err
}

// RecordTransition implements JobStore.
func (s *PostgresStore) RecordTransition(ctx context.Context, jobID uuid.UUID, from, to string, metadata map[string]any) error {
	meta, _ := json.Marshal(metadata)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cp.job_state_transitions (id, job_id, from_state, to_state, at, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.New().String(), jobID.String(), from, to, time.Now().UTC(), meta,
	)
	return err
}

// CreateIdempotencyKey implements JobStore. Returns the new key's ID.
func (s *PostgresStore) CreateIdempotencyKey(ctx context.Context, key, userID string, jobID uuid.UUID, ttl time.Duration) (keyID uuid.UUID, err error) {
	keyID = uuid.New()
	exp := time.Now().UTC().Add(ttl)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO cp.idempotency_keys (id, key, job_id, user_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		keyID.String(), key, jobID.String(), userID, time.Now().UTC(), exp,
	)
	return keyID, err
}

// GetIdempotencyKey implements JobStore.
func (s *PostgresStore) GetIdempotencyKey(ctx context.Context, key, userID string) (jobID uuid.UUID, ok bool, err error) {
	j, uid, ok, err := s.GetIdempotencyKeyByKey(ctx, key)
	if err != nil || !ok {
		return uuid.Nil, false, err
	}
	if uid != userID {
		return uuid.Nil, false, nil
	}
	return j, true, nil
}

// GetIdempotencyKeyByKey implements JobStore.
func (s *PostgresStore) GetIdempotencyKeyByKey(ctx context.Context, key string) (jobID uuid.UUID, userID string, ok bool, err error) {
	var j string
	e := s.db.QueryRowContext(ctx, `
		SELECT job_id, user_id FROM cp.idempotency_keys WHERE key = $1 AND expires_at > NOW()`,
		key,
	).Scan(&j, &userID)
	if e == sql.ErrNoRows {
		return uuid.Nil, "", false, nil
	}
	if e != nil {
		return uuid.Nil, "", false, e
	}
	jobID, _ = uuid.Parse(j)
	return jobID, userID, true, nil
}

// ListJobs implements JobStore.
func (s *PostgresStore) ListJobs(ctx context.Context, userID string, jobType string, limit, offset int) ([]*Job, error) {
	var query string
	var args []interface{}
	
		if jobType != "" {
		query = `
			SELECT id, user_id, type, status, idempotency_key_id, payload, result, error, progress, current_section, created_at, updated_at
			FROM cp.jobs 
			WHERE user_id = $1 AND type = $2 
			ORDER BY created_at DESC 
			LIMIT $3 OFFSET $4`
		args = []interface{}{userID, jobType, limit, offset}
	} else {
		query = `
			SELECT id, user_id, type, status, idempotency_key_id, payload, result, error, progress, current_section, created_at, updated_at
			FROM cp.jobs 
			WHERE user_id = $1 
			ORDER BY created_at DESC 
			LIMIT $2 OFFSET $3`
		args = []interface{}{userID, limit, offset}
	}
	
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var jobs []*Job
	for rows.Next() {
		var j Job
		var idStr string
		var payload, result []byte
		var errMsg sql.NullString
		var keyID sql.NullString
		var progress sql.NullInt64
		var currentSection sql.NullString
		
		if err := rows.Scan(&idStr, &j.UserID, &j.Type, &j.Status, &keyID, &payload, &result, &errMsg, &progress, &currentSection, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		
		j.ID, _ = uuid.Parse(idStr)
		j.Payload = payload
		j.Result = result
		if errMsg.Valid {
			j.Error = &errMsg.String
		}
		if keyID.Valid {
			u, _ := uuid.Parse(keyID.String)
			j.IdempotencyKeyID = &u
		}
		if progress.Valid {
			p := int(progress.Int64)
			j.Progress = &p
		}
		if currentSection.Valid {
			j.CurrentSection = &currentSection.String
		}
		jobs = append(jobs, &j)
	}
	
	if err := rows.Err(); err != nil {
		return nil, err
	}
	
	return jobs, nil
}

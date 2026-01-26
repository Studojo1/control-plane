package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// PaymentStore persistence for payments.
type PaymentStore interface {
	CreatePayment(ctx context.Context, p *Payment) error
	GetPaymentByOrderID(ctx context.Context, orderID string) (*Payment, error)
	GetPaymentByJobID(ctx context.Context, jobID uuid.UUID) (*Payment, error)
	UpdatePayment(ctx context.Context, id uuid.UUID, paymentID *string, status string) error
	LinkPaymentToJob(ctx context.Context, paymentID, jobID uuid.UUID) error
	VerifyPaymentForJob(ctx context.Context, userID string, jobID uuid.UUID) (bool, error)
}

// Payment domain model.
type Payment struct {
	ID               uuid.UUID
	UserID           string
	JobID            *uuid.UUID
	RazorpayOrderID  string
	RazorpayPaymentID *string
	Amount           int // Amount in paise (e.g., 13900 for ₹139)
	Status           string // 'pending', 'completed', 'failed', 'refunded'
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CreatePayment implements PaymentStore.
func (s *PostgresStore) CreatePayment(ctx context.Context, p *Payment) error {
	var jobID interface{}
	if p.JobID != nil {
		jobID = p.JobID.String()
	}
	var paymentID interface{}
	if p.RazorpayPaymentID != nil {
		paymentID = *p.RazorpayPaymentID
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cp.payments (id, user_id, job_id, razorpay_order_id, razorpay_payment_id, amount, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		p.ID.String(), p.UserID, jobID, p.RazorpayOrderID, paymentID, p.Amount, p.Status, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

// GetPaymentByOrderID implements PaymentStore.
func (s *PostgresStore) GetPaymentByOrderID(ctx context.Context, orderID string) (*Payment, error) {
	var p Payment
	var jobID sql.NullString
	var paymentID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, job_id, razorpay_order_id, razorpay_payment_id, amount, status, created_at, updated_at
		FROM cp.payments
		WHERE razorpay_order_id = $1`,
		orderID,
	).Scan(
		&p.ID, &p.UserID, &jobID, &p.RazorpayOrderID, &paymentID, &p.Amount, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if jobID.Valid {
		id, err := uuid.Parse(jobID.String)
		if err == nil {
			p.JobID = &id
		}
	}
	if paymentID.Valid {
		p.RazorpayPaymentID = &paymentID.String
	}
	return &p, nil
}

// GetPaymentByJobID implements PaymentStore.
func (s *PostgresStore) GetPaymentByJobID(ctx context.Context, jobID uuid.UUID) (*Payment, error) {
	var p Payment
	var jobIDStr sql.NullString
	var paymentID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, job_id, razorpay_order_id, razorpay_payment_id, amount, status, created_at, updated_at
		FROM cp.payments
		WHERE job_id = $1`,
		jobID.String(),
	).Scan(
		&p.ID, &p.UserID, &jobIDStr, &p.RazorpayOrderID, &paymentID, &p.Amount, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if jobIDStr.Valid {
		id, err := uuid.Parse(jobIDStr.String)
		if err == nil {
			p.JobID = &id
		}
	}
	if paymentID.Valid {
		p.RazorpayPaymentID = &paymentID.String
	}
	return &p, nil
}

// UpdatePayment implements PaymentStore.
func (s *PostgresStore) UpdatePayment(ctx context.Context, id uuid.UUID, paymentID *string, status string) error {
	var paymentIDVal interface{}
	if paymentID != nil {
		paymentIDVal = *paymentID
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE cp.payments
		SET razorpay_payment_id = COALESCE($1, razorpay_payment_id),
		    status = $2,
		    updated_at = $3
		WHERE id = $4`,
		paymentIDVal, status, time.Now().UTC(), id.String(),
	)
	return err
}

// LinkPaymentToJob links a payment to a job.
func (s *PostgresStore) LinkPaymentToJob(ctx context.Context, paymentID, jobID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cp.payments
		SET job_id = $1, updated_at = $2
		WHERE id = $3 AND job_id IS NULL`,
		jobID.String(), time.Now().UTC(), paymentID.String(),
	)
	return err
}

// VerifyPaymentForJob implements PaymentStore.
func (s *PostgresStore) VerifyPaymentForJob(ctx context.Context, userID string, jobID uuid.UUID) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM cp.payments
		WHERE user_id = $1 AND job_id = $2 AND status = 'completed'`,
		userID, jobID.String(),
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

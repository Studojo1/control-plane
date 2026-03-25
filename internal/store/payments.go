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
	GetPaymentByDodoCheckoutID(ctx context.Context, checkoutID string) (*Payment, error)
	GetPaymentByJobID(ctx context.Context, jobID uuid.UUID) (*Payment, error)
	UpdatePayment(ctx context.Context, id uuid.UUID, paymentID *string, status string) error
	UpdateDodoPayment(ctx context.Context, id uuid.UUID, dodoPaymentID string, status string) error
	LinkPaymentToJob(ctx context.Context, paymentID, jobID uuid.UUID) error
	VerifyPaymentForJob(ctx context.Context, userID string, jobID uuid.UUID) (bool, error)
}

// Payment domain model.
type Payment struct {
	ID                uuid.UUID
	UserID            string
	JobID             *uuid.UUID
	Provider          string  // "razorpay" or "dodo"
	RazorpayOrderID   string
	RazorpayPaymentID *string
	DodoCheckoutID    *string
	DodoPaymentID     *string
	Amount            int    // Amount in smallest unit (paise for INR, cents for USD)
	Currency          string // "INR" or "USD"
	GeoCountry        *string
	Status            string // 'pending', 'completed', 'failed', 'refunded'
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// scanPayment scans a payment row from the database (shared helper for all queries).
func scanPayment(row interface{ Scan(dest ...any) error }) (*Payment, error) {
	var p Payment
	var jobID, paymentID, dodoCheckoutID, dodoPaymentID, geoCountry sql.NullString
	err := row.Scan(
		&p.ID, &p.UserID, &jobID, &p.Provider, &p.RazorpayOrderID, &paymentID,
		&dodoCheckoutID, &dodoPaymentID, &p.Amount, &p.Currency, &geoCountry,
		&p.Status, &p.CreatedAt, &p.UpdatedAt,
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
	if dodoCheckoutID.Valid {
		p.DodoCheckoutID = &dodoCheckoutID.String
	}
	if dodoPaymentID.Valid {
		p.DodoPaymentID = &dodoPaymentID.String
	}
	if geoCountry.Valid {
		p.GeoCountry = &geoCountry.String
	}
	return &p, nil
}

const selectPaymentCols = `id, user_id, job_id, provider, razorpay_order_id, razorpay_payment_id,
	dodo_checkout_id, dodo_payment_id, amount, currency, geo_country, status, created_at, updated_at`

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
	var dodoCheckoutID interface{}
	if p.DodoCheckoutID != nil {
		dodoCheckoutID = *p.DodoCheckoutID
	}
	var dodoPaymentID interface{}
	if p.DodoPaymentID != nil {
		dodoPaymentID = *p.DodoPaymentID
	}
	var geoCountry interface{}
	if p.GeoCountry != nil {
		geoCountry = *p.GeoCountry
	}

	provider := p.Provider
	if provider == "" {
		provider = "razorpay"
	}
	currency := p.Currency
	if currency == "" {
		currency = "INR"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cp.payments (id, user_id, job_id, provider, razorpay_order_id, razorpay_payment_id,
			dodo_checkout_id, dodo_payment_id, amount, currency, geo_country, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		p.ID.String(), p.UserID, jobID, provider, p.RazorpayOrderID, paymentID,
		dodoCheckoutID, dodoPaymentID, p.Amount, currency, geoCountry, p.Status, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

// GetPaymentByOrderID implements PaymentStore.
func (s *PostgresStore) GetPaymentByOrderID(ctx context.Context, orderID string) (*Payment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+selectPaymentCols+`
		FROM cp.payments
		WHERE razorpay_order_id = $1`,
		orderID,
	)
	return scanPayment(row)
}

// GetPaymentByDodoCheckoutID implements PaymentStore.
func (s *PostgresStore) GetPaymentByDodoCheckoutID(ctx context.Context, checkoutID string) (*Payment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+selectPaymentCols+`
		FROM cp.payments
		WHERE dodo_checkout_id = $1`,
		checkoutID,
	)
	return scanPayment(row)
}

// GetPaymentByJobID implements PaymentStore.
func (s *PostgresStore) GetPaymentByJobID(ctx context.Context, jobID uuid.UUID) (*Payment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+selectPaymentCols+`
		FROM cp.payments
		WHERE job_id = $1`,
		jobID.String(),
	)
	return scanPayment(row)
}

// UpdatePayment implements PaymentStore (Razorpay).
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

// UpdateDodoPayment implements PaymentStore (Dodo).
func (s *PostgresStore) UpdateDodoPayment(ctx context.Context, id uuid.UUID, dodoPaymentID string, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cp.payments
		SET dodo_payment_id = $1,
		    status = $2,
		    updated_at = $3
		WHERE id = $4`,
		dodoPaymentID, status, time.Now().UTC(), id.String(),
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

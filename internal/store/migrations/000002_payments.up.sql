CREATE TABLE cp.payments (
    id UUID PRIMARY KEY,
    user_id TEXT NOT NULL,
    job_id UUID REFERENCES cp.jobs(id) ON DELETE SET NULL,
    razorpay_order_id TEXT NOT NULL,
    razorpay_payment_id TEXT,
    amount INTEGER NOT NULL, -- Amount in paise (e.g., 13900 for ₹139)
    status TEXT NOT NULL, -- 'pending', 'completed', 'failed', 'refunded'
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_payments_user_created ON cp.payments (user_id, created_at DESC);
CREATE INDEX idx_payments_job ON cp.payments (job_id) WHERE job_id IS NOT NULL;
CREATE INDEX idx_payments_razorpay_order ON cp.payments (razorpay_order_id);
CREATE INDEX idx_payments_status ON cp.payments (status);

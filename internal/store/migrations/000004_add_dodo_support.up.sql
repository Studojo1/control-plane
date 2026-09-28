ALTER TABLE cp.payments
  ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'razorpay',
  ADD COLUMN IF NOT EXISTS dodo_checkout_id TEXT,
  ADD COLUMN IF NOT EXISTS dodo_payment_id TEXT,
  ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT 'INR',
  ADD COLUMN IF NOT EXISTS geo_country TEXT;

CREATE INDEX IF NOT EXISTS idx_payments_dodo_checkout ON cp.payments(dodo_checkout_id) WHERE dodo_checkout_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payments_provider ON cp.payments(provider);

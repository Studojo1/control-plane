DROP INDEX IF EXISTS cp.idx_payments_dodo_checkout;
DROP INDEX IF EXISTS cp.idx_payments_provider;

ALTER TABLE cp.payments
  DROP COLUMN IF EXISTS provider,
  DROP COLUMN IF EXISTS dodo_checkout_id,
  DROP COLUMN IF EXISTS dodo_payment_id,
  DROP COLUMN IF EXISTS currency,
  DROP COLUMN IF EXISTS geo_country;

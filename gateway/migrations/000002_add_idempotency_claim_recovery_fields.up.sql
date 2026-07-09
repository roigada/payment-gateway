ALTER TABLE idempotency_records
    ADD COLUMN IF NOT EXISTS payment_id text CHECK (payment_id IS NULL OR payment_id ~ '^pay_[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$');

ALTER TABLE idempotency_records
    ADD COLUMN IF NOT EXISTS claimed_at timestamptz;

UPDATE idempotency_records
   SET claimed_at = created_at
 WHERE claimed_at IS NULL;

ALTER TABLE idempotency_records
    ALTER COLUMN claimed_at SET DEFAULT now(),
    ALTER COLUMN claimed_at SET NOT NULL;

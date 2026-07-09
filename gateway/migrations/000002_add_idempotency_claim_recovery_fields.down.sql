ALTER TABLE idempotency_records
    DROP COLUMN IF EXISTS claimed_at,
    DROP COLUMN IF EXISTS payment_id;

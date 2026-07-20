DROP INDEX IF EXISTS idempotency_records_completed_at_idx;

ALTER TABLE idempotency_records
    DROP CONSTRAINT idempotency_records_completion_check,
    ADD CONSTRAINT idempotency_records_completion_check CHECK (
        (status = 'in_progress' AND http_status IS NULL AND payment_result IS NULL)
        OR
        (status = 'completed' AND http_status IS NOT NULL AND payment_result IS NOT NULL AND jsonb_typeof(payment_result) = 'object')
    ),
    DROP COLUMN completed_at;

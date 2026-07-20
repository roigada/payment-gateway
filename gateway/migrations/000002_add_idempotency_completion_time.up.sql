ALTER TABLE idempotency_records
    ADD COLUMN completed_at timestamptz;

UPDATE idempotency_records
   SET completed_at = now()
 WHERE status = 'completed';

ALTER TABLE idempotency_records
    DROP CONSTRAINT idempotency_records_completion_check,
    ADD CONSTRAINT idempotency_records_completion_check CHECK (
        (status = 'in_progress' AND http_status IS NULL AND payment_result IS NULL AND completed_at IS NULL)
        OR
        (status = 'completed' AND http_status IS NOT NULL AND payment_result IS NOT NULL AND jsonb_typeof(payment_result) = 'object' AND completed_at IS NOT NULL)
    );

CREATE INDEX idempotency_records_completed_at_idx
    ON idempotency_records (completed_at)
 WHERE status = 'completed';

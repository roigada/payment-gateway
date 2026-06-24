ALTER TABLE idempotency_records
    DROP CONSTRAINT IF EXISTS idempotency_records_completion_check,
    DROP CONSTRAINT IF EXISTS idempotency_records_response_status_check,
    DROP CONSTRAINT IF EXISTS idempotency_records_status_check;

DELETE FROM idempotency_records
 WHERE status = 'in_progress';

ALTER TABLE idempotency_records
    ALTER COLUMN response_body SET NOT NULL,
    ADD CONSTRAINT idempotency_records_response_body_check CHECK (jsonb_typeof(response_body) = 'object'),
    DROP COLUMN response_status,
    DROP COLUMN status;

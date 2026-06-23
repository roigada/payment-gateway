ALTER TABLE idempotency_records
    ADD COLUMN status text,
    ADD COLUMN response_status integer;

UPDATE idempotency_records
   SET status = 'completed',
       response_status = CASE
           WHEN operation = 'authorize_payment' THEN 201
           ELSE 200
       END;

ALTER TABLE idempotency_records
    ALTER COLUMN status SET NOT NULL,
    DROP CONSTRAINT IF EXISTS idempotency_records_response_body_check,
    ALTER COLUMN response_body DROP NOT NULL,
    ALTER COLUMN response_status DROP NOT NULL,
    ADD CONSTRAINT idempotency_records_status_check CHECK (status IN ('in_progress', 'completed')),
    ADD CONSTRAINT idempotency_records_response_status_check CHECK (response_status IS NULL OR response_status BETWEEN 100 AND 599),
    ADD CONSTRAINT idempotency_records_completion_check CHECK (
        (status = 'in_progress' AND response_status IS NULL AND response_body IS NULL)
        OR
        (status = 'completed' AND response_status IS NOT NULL AND response_body IS NOT NULL AND jsonb_typeof(response_body) = 'object')
    );

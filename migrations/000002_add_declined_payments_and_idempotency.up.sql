ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_bank_authorization_id_check;

ALTER TABLE payments ALTER COLUMN bank_authorization_id DROP NOT NULL;

ALTER TABLE payments
    ADD COLUMN decline_reason text,
    ADD CONSTRAINT payments_status_check CHECK (status IN ('authorized', 'declined')),
    ADD CONSTRAINT payments_bank_authorization_id_check CHECK (bank_authorization_id IS NULL OR length(trim(bank_authorization_id)) > 0),
    ADD CONSTRAINT payments_decline_reason_check CHECK (decline_reason IS NULL OR decline_reason IN ('insufficient_funds', 'invalid_card', 'expired_card', 'unknown')),
    ADD CONSTRAINT payments_status_private_fields_check CHECK (
        (status = 'authorized' AND bank_authorization_id IS NOT NULL AND decline_reason IS NULL)
        OR
        (status = 'declined' AND bank_authorization_id IS NULL AND decline_reason IS NOT NULL)
    );

CREATE TABLE IF NOT EXISTS idempotency_records (
    operation text NOT NULL CHECK (length(trim(operation)) > 0),
    key text NOT NULL CHECK (length(trim(key)) > 0),
    request_fingerprint text NOT NULL CHECK (length(trim(request_fingerprint)) > 0),
    response_body jsonb NOT NULL CHECK (jsonb_typeof(response_body) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (operation, key)
);

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_private_fields_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_capture_bank_operation_key_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_bank_capture_id_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;

ALTER TABLE payments
    DROP COLUMN IF EXISTS capture_bank_operation_key,
    DROP COLUMN IF EXISTS bank_capture_id,
    ADD CONSTRAINT payments_status_check CHECK (status IN ('authorized', 'declined')),
    ADD CONSTRAINT payments_status_private_fields_check CHECK (
        (status = 'authorized' AND bank_authorization_id IS NOT NULL AND decline_reason IS NULL)
        OR
        (status = 'declined' AND bank_authorization_id IS NULL AND decline_reason IS NOT NULL)
    );

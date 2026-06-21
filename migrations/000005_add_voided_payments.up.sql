ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_private_fields_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;

ALTER TABLE payments
    ADD COLUMN bank_void_id text,
    ADD COLUMN void_bank_operation_key text,
    ADD CONSTRAINT payments_status_check CHECK (status IN ('pending', 'authorized', 'declined', 'captured', 'voided', 'refunded')),
    ADD CONSTRAINT payments_bank_void_id_check CHECK (bank_void_id IS NULL OR length(trim(bank_void_id)) > 0),
    ADD CONSTRAINT payments_void_bank_operation_key_check CHECK (void_bank_operation_key IS NULL OR length(trim(void_bank_operation_key)) > 0),
    ADD CONSTRAINT payments_status_private_fields_check CHECK (
        (status = 'pending' AND bank_authorization_id IS NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'authorized' AND bank_authorization_id IS NOT NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NULL)
        OR
        (status = 'declined' AND bank_authorization_id IS NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND decline_reason IS NOT NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'captured' AND bank_authorization_id IS NOT NULL AND bank_capture_id IS NOT NULL AND capture_bank_operation_key IS NOT NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'voided' AND bank_authorization_id IS NOT NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NOT NULL AND void_bank_operation_key IS NOT NULL)
        OR
        (status = 'refunded' AND bank_authorization_id IS NOT NULL AND bank_capture_id IS NOT NULL AND capture_bank_operation_key IS NOT NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
    );

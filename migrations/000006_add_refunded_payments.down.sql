ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_private_fields_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_refund_bank_operation_key_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_bank_refund_id_check;

ALTER TABLE payments
    DROP COLUMN IF EXISTS refund_bank_operation_key,
    DROP COLUMN IF EXISTS bank_refund_id,
    ADD CONSTRAINT payments_status_private_fields_check CHECK (
        (status = 'pending' AND bank_authorization_id IS NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'authorized' AND bank_authorization_id IS NOT NULL AND bank_capture_id IS NULL AND decline_reason IS NULL AND bank_void_id IS NULL)
        OR
        (status = 'declined' AND bank_authorization_id IS NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND decline_reason IS NOT NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'captured' AND bank_authorization_id IS NOT NULL AND bank_capture_id IS NOT NULL AND capture_bank_operation_key IS NOT NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'voided' AND bank_authorization_id IS NOT NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NOT NULL AND void_bank_operation_key IS NOT NULL)
        OR
        (status = 'refunded' AND bank_authorization_id IS NOT NULL AND bank_capture_id IS NOT NULL AND capture_bank_operation_key IS NOT NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
    );

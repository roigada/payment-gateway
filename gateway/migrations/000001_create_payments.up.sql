CREATE TABLE IF NOT EXISTS payments (
    id text PRIMARY KEY CHECK (id ~ '^pay_[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'),
    order_id text NOT NULL CHECK (length(trim(order_id)) > 0),
    customer_id text NOT NULL CHECK (length(trim(customer_id)) > 0),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    currency text NOT NULL CHECK (currency = 'USD'),
    status text NOT NULL CHECK (status IN ('pending', 'authorized', 'expired', 'declined', 'captured', 'voided', 'refunded')),
    bank_authorization_id text CHECK (bank_authorization_id IS NULL OR length(trim(bank_authorization_id)) > 0),
    authorization_expires_at timestamptz,
    authorization_bank_operation_key text NOT NULL CHECK (length(trim(authorization_bank_operation_key)) > 0),
    authorization_card_fingerprint text NOT NULL CHECK (length(trim(authorization_card_fingerprint)) > 0),
    bank_capture_id text CHECK (bank_capture_id IS NULL OR length(trim(bank_capture_id)) > 0),
    capture_bank_operation_key text CHECK (capture_bank_operation_key IS NULL OR length(trim(capture_bank_operation_key)) > 0),
    bank_refund_id text CHECK (bank_refund_id IS NULL OR length(trim(bank_refund_id)) > 0),
    refund_bank_operation_key text CHECK (refund_bank_operation_key IS NULL OR length(trim(refund_bank_operation_key)) > 0),
    bank_void_id text CHECK (bank_void_id IS NULL OR length(trim(bank_void_id)) > 0),
    void_bank_operation_key text CHECK (void_bank_operation_key IS NULL OR length(trim(void_bank_operation_key)) > 0),
    decline_reason text CHECK (decline_reason IS NULL OR decline_reason IN ('insufficient_funds', 'invalid_card', 'expired_card', 'unknown')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT payments_status_private_fields_check CHECK (
        (status = 'pending' AND bank_authorization_id IS NULL AND authorization_expires_at IS NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND bank_refund_id IS NULL AND refund_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'authorized' AND bank_authorization_id IS NOT NULL AND authorization_expires_at IS NOT NULL AND bank_capture_id IS NULL AND bank_refund_id IS NULL AND refund_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NULL)
        OR
        (status = 'expired' AND bank_authorization_id IS NOT NULL AND authorization_expires_at IS NOT NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND bank_refund_id IS NULL AND refund_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'declined' AND bank_authorization_id IS NULL AND authorization_expires_at IS NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND bank_refund_id IS NULL AND refund_bank_operation_key IS NULL AND decline_reason IS NOT NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'captured' AND bank_authorization_id IS NOT NULL AND authorization_expires_at IS NOT NULL AND bank_capture_id IS NOT NULL AND capture_bank_operation_key IS NOT NULL AND bank_refund_id IS NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
        OR
        (status = 'voided' AND bank_authorization_id IS NOT NULL AND authorization_expires_at IS NOT NULL AND bank_capture_id IS NULL AND capture_bank_operation_key IS NULL AND bank_refund_id IS NULL AND refund_bank_operation_key IS NULL AND decline_reason IS NULL AND bank_void_id IS NOT NULL AND void_bank_operation_key IS NOT NULL)
        OR
        (status = 'refunded' AND bank_authorization_id IS NOT NULL AND authorization_expires_at IS NOT NULL AND bank_capture_id IS NOT NULL AND capture_bank_operation_key IS NOT NULL AND bank_refund_id IS NOT NULL AND refund_bank_operation_key IS NOT NULL AND decline_reason IS NULL AND bank_void_id IS NULL AND void_bank_operation_key IS NULL)
    )
);

CREATE TABLE IF NOT EXISTS idempotency_records (
    operation text NOT NULL CHECK (length(trim(operation)) > 0),
    key text NOT NULL CHECK (length(trim(key)) > 0),
    request_fingerprint text NOT NULL CHECK (length(trim(request_fingerprint)) > 0),
    payment_id text CHECK (payment_id IS NULL OR payment_id ~ '^pay_[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'),
    status text NOT NULL CHECK (status IN ('in_progress', 'completed')),
    failure_kind text CHECK (failure_kind IS NULL OR failure_kind IN ('authorization_expired')),
    payment_result jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (operation, key),
    CONSTRAINT idempotency_records_completion_check CHECK (
        (status = 'in_progress' AND failure_kind IS NULL AND payment_result IS NULL AND completed_at IS NULL)
        OR
        (status = 'completed' AND payment_result IS NOT NULL AND jsonb_typeof(payment_result) = 'object' AND completed_at IS NOT NULL)
    )
);

CREATE INDEX idempotency_records_completed_at_idx
    ON idempotency_records (completed_at)
 WHERE status = 'completed';

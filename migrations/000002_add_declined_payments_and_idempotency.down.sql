DROP TABLE IF EXISTS idempotency_records;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_private_fields_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_decline_reason_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_bank_authorization_id_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;

ALTER TABLE payments DROP COLUMN IF EXISTS decline_reason;
ALTER TABLE payments ALTER COLUMN bank_authorization_id SET NOT NULL;

ALTER TABLE payments
    ADD CONSTRAINT payments_status_check CHECK (status IN ('authorized')),
    ADD CONSTRAINT payments_bank_authorization_id_check CHECK (length(trim(bank_authorization_id)) > 0);

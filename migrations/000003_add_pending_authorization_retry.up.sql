ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_private_fields_check;

ALTER TABLE payments
    ADD COLUMN authorization_card_fingerprint text;

UPDATE payments
   SET authorization_card_fingerprint = 'legacy_authorization_card_fingerprint'
 WHERE authorization_card_fingerprint IS NULL;

ALTER TABLE payments
    ALTER COLUMN authorization_card_fingerprint SET NOT NULL,
    ADD CONSTRAINT payments_authorization_card_fingerprint_check CHECK (length(trim(authorization_card_fingerprint)) > 0),
    ADD CONSTRAINT payments_status_check CHECK (status IN ('pending', 'authorized', 'declined')),
    ADD CONSTRAINT payments_status_private_fields_check CHECK (
        (status = 'pending' AND bank_authorization_id IS NULL AND decline_reason IS NULL)
        OR
        (status = 'authorized' AND bank_authorization_id IS NOT NULL AND decline_reason IS NULL)
        OR
        (status = 'declined' AND bank_authorization_id IS NULL AND decline_reason IS NOT NULL)
    );

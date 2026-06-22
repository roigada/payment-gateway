# Authorization card fingerprints

The gateway uses two different fingerprint concepts during authorization. The authorization request fingerprint belongs to public idempotency and covers the authorization request values, including amount, so a repeated idempotency key can only replay the same request. The authorization card fingerprint belongs to Pending authorization retry and is stored on the Payment.

Pending authorization retries are submitted for an existing Payment ID with card details, so the gateway stores an authorization card fingerprint and rejects retries whose card fingerprint differs. The card fingerprint is computed with a configured HMAC secret over normalized card number and expiry, deliberately excluding CVV and raw card storage; this is safer than a plain hash while avoiding persistent sensitive card details in the learning project. The Payment already owns the amount, order, and customer, so those values are not part of the retry card check.

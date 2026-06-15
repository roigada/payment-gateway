# Authorization retry fingerprints

Pending authorization retries must represent the same authorization attempt, so the gateway stores an authorization fingerprint and rejects retries whose fingerprint differs. The fingerprint is computed with a configured HMAC secret over normalized card number, expiry, and amount, deliberately excluding CVV and raw card storage; this is safer than a plain hash while avoiding persistent sensitive card details in the learning project. A different card attempt should create a new Payment for the same Order ID.

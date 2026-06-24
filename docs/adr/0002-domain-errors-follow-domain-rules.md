# Domain errors follow domain rules

Payment invariant errors live with the domain model, while use-case outcomes live in the application layer. Errors such as `ErrInvalidPaymentID`, `ErrInvalidPaymentAmount`, and `ErrInvalidPaymentStatus` belong to `internal/domain` because they describe Payment rules. Outcomes such as a missing Payment, an idempotency conflict, or a Mock Bank timeout belong to `internal/app` because adapters translate those use-case results into transport-specific responses.

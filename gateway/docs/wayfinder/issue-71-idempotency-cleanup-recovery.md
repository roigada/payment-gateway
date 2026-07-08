# Assess Idempotency Cleanup and Stuck In-Progress Recovery

Research ticket: [Assess idempotency cleanup and stuck in-progress recovery](https://github.com/roigada/payment-gateway/issues/71)

## Question

Should the roadmap include TTL, cleanup, admin repair, or command-level recovery for public idempotency records that remain in_progress after process failure, and what design would preserve the gateway's existing bank operation key recovery guarantees?

## Local Findings

- Public idempotency records live in `idempotency_records` with `operation`, `key`, `request_fingerprint`, `status`, optional replay snapshot, and `created_at`. There is no expiry, lease owner, attempt timestamp, or recovery state beyond `in_progress` and `completed`. Source: [gateway/migrations/000001_create_payments.up.sql](../../migrations/000001_create_payments.up.sql).
- `ClaimPaymentCommand` first inserts an `in_progress` idempotency row. If the row already exists with the same fingerprint and `in_progress` status, the store returns `idempotency_in_progress`; it does not attempt to recover the command. Source: [gateway/internal/postgres/payment_store.go](../../internal/postgres/payment_store.go).
- A normal transient error before a durable bank/business result releases the `in_progress` row by deleting it. A process crash skips that release and leaves the public key permanently blocked. Source: [gateway/internal/app/payment_service.go](../../internal/app/payment_service.go), [ADR-0019](../adr/0019-payment-store-owns-command-persistence.md).
- The gateway already stores bank recovery facts before outbound Mock Bank calls: initial authorization stores the Payment with an authorization Bank Operation Key, and capture/void/refund claims persist their specific Bank Operation Key before the bank call. Source: [ADR-0012](../adr/0012-do-not-hold-database-transactions-across-bank-calls.md), [ADR-0019](../adr/0019-payment-store-owns-command-persistence.md).
- The strongest existing reliability invariant is atomic local completion: a completed bank/business outcome must not persist the Payment transition while leaving the public idempotency record `in_progress`. Source: [ADR-0019](../adr/0019-payment-store-owns-command-persistence.md).
- Capture and void have an extra expiration rule: a new post-expiration command is blocked, but a retry with an already-saved Bank Operation Key may still call the Mock Bank after expiration to recover a pre-expiration bank call. Source: [ADR-0017](../adr/0017-authorization-expiration-is-payment-lifecycle-state.md).
- The Mock Bank has its own idempotency cleanup for its internal `idempotency_keys`, but that is separate bundled demo infrastructure and should not drive gateway public idempotency design. Source: [mock-bank/bank/internal/repository/idempotency.go](../../../mock-bank/bank/internal/repository/idempotency.go).

## Options Considered

1. **TTL-delete stale `in_progress` public idempotency records**
   - Simple to explain and cheap to implement.
   - Unsafe as the primary recovery path. If the gateway deletes a stale row and accepts the same public key as a fresh command, it can generate or accept a different recovery path instead of reusing the durable Bank Operation Key already associated with the original attempt.
   - Especially risky for capture, void, and refund because the bank side effect may already have happened and must be recovered with the saved operation key.

2. **Admin-only repair that deletes or marks stale claims**
   - Useful as an operational backstop and portfolio signal if it is audit-friendly.
   - Not sufficient as the main user-facing recovery path. A reviewer would still see a crash scenario where normal client retry remains stuck until manual intervention.
   - Manual repair must not allow arbitrary deletion before checking the Payment's stored Bank Operation Key and current Payment Status.

3. **Command-level stale-claim recovery**
   - On a same-operation, same-key, same-fingerprint retry, the store can detect that an `in_progress` claim is older than a configured stale threshold, lock the relevant rows, reconstruct the command claim from persisted recovery facts, and let the use case call the Mock Bank again with the same Bank Operation Key.
   - This preserves the existing model: the gateway still does not hold database transactions across bank calls, and bank recovery remains tied to stored Bank Operation Keys.
   - It also produces strong backend portfolio signal because it demonstrates crash recovery, idempotent side-effect handling, row locking, and explicit operational semantics.

4. **Background recovery worker**
   - Potentially useful later for automatic cleanup or repair, but it is a larger operational feature.
   - It is not the first portfolio step because the gateway can get the core correctness signal from request-driven stale-claim recovery with metrics and tests.

5. **TTL for completed replay snapshots only**
   - Safe as retention hygiene because completed records already contain durable replay snapshots and no longer protect an unknown in-flight bank call.
   - Useful but lower priority than stale `in_progress` recovery. It should be scoped separately as data-retention cleanup, not mixed with crash recovery.

## Decision

The roadmap should include **command-level stale-claim recovery for `in_progress` public idempotency records** as the main reliability feature. TTL deletion by itself should not be used for stale `in_progress` rows because it can discard the public command guard before the gateway has recovered the bank result with the correct Bank Operation Key.

Recommended design for a later implementation spec:

1. Add an `updated_at` or `claimed_at` timestamp to `idempotency_records`, and define a conservative stale threshold in configuration.
2. Extend `ClaimPaymentCommand` so a same-operation, same-key, same-fingerprint `in_progress` record older than the threshold can be reclaimed under database locks.
3. Reconstruct the claim from persisted Payment data and stored Bank Operation Keys instead of generating a new Bank Operation Key for recovery.
4. Preserve operation-specific rules:
   - authorization start reuses the Payment's authorization Bank Operation Key and card fingerprint.
   - authorization retry reuses the Payment's authorization Bank Operation Key while enforcing the same authorization card fingerprint rule.
   - capture, void, and refund reuse their persisted command-specific Bank Operation Key.
   - capture and void keep the ADR-0017 expiration exception for already-saved Bank Operation Keys.
5. Complete the recovered command through the existing `CompletePaymentCommand` path so Payment transition and replay snapshot still commit atomically.
6. Add bounded metrics or logs for stale claim recovery attempts, successes, and unrecoverable conflicts.
7. Add an admin/read-only inspection command or endpoint only as a secondary operational aid; avoid manual deletion as the normal recovery mechanism.
8. Treat TTL cleanup for `completed` replay records as separate lower-priority retention work.

## Implications for Later Specs

- The highest-signal implementation ticket should be framed as crash recovery for public idempotency, not as generic cleanup.
- Tests should cover a process-crash shape: claim persisted, Bank Operation Key persisted, public idempotency row still `in_progress`, then same public retry recovers through the Mock Bank using the original Bank Operation Key and completes the replay snapshot.
- Postgres tests should prove stale detection is locked and fingerprint-safe; app tests should prove the bank receives the original operation key.
- The design may require a small `PaymentCommandClaim` extension so the application can distinguish a freshly claimed command from a recovered stale claim only for metrics, not for business behavior.
- This decision complements, but does not replace, the separate Pending authorization recovery question. Pending recovery can decide whether the gateway needs automatic/background resolution for unresolved authorization outcomes after this command-level foundation is ranked.

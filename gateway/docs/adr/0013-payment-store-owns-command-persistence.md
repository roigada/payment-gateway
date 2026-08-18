# PaymentStore owns command persistence

Payment command persistence crosses Payment rows and public idempotency rows, so the application layer uses one broad `PaymentStore` port instead of separate payment and idempotency repositories.

The Postgres adapter exposes this as one concrete `PaymentStore`. It does not expose a separate idempotency repository or row-level payment mutation repository for command flows. Idempotency rows are still stored in Postgres, but their SQL is an internal implementation detail of the store.

`PaymentStore` owns command persistence through two intention-specific claim methods plus shared claim-handle completion and release methods:

- `ClaimAuthorizationStart` accepts an `AuthorizationStartClaimRequest` and claims the public idempotency slot while creating, or recovering, the new **Payment** being authorized. The request owns a new Pending **Payment**.
- `ClaimExistingPaymentCommand` accepts an `ExistingPaymentCommandClaimRequest` for Authorization Retry, Capture, Void, or Refund. It claims the public idempotency slot while loading, locking, and validating an existing **Payment**, then persists or reuses its pre-bank recovery facts. The store encodes command-specific persistence invariants, including expected **Payment Status** checks, authorization card fingerprint checks, and capture/void/refund **Bank Operation Key** persistence.
- `PaymentCommandClaim` is the opaque handle returned by either claim method. It hides idempotency record and status details, carries the Payment for a new bank call, returns a stored replay result for replay, and is passed back for completion or release.
- `CompletePaymentCommand` saves the Payment transition attached to the claim and completes the replay snapshot in the same database transaction after a definitive bank result.
- `ReleasePaymentCommand` releases the `in_progress` public idempotency claim represented by the claim handle after transient post-claim failures.

The store also exposes payment query methods because queries and non-command lifecycle refreshes still need the same Payment persistence boundary.

The application layer must not know about `sql.Tx`, `*sql.DB`, table names, row locks, transaction mechanics, or which persistence fields must be combined for each command claim. Those details are Postgres adapter concerns. This keeps the use case code focused on Payment rules, public replay behavior, and Mock Bank calls.

This decision does not contradict ADR-0007. The Postgres adapter may use short internal database transactions around local persistence, but it must not hold a transaction across a Mock Bank call. Payment commands therefore use one transaction before the bank call to persist the claim and recovery facts, then a separate transaction after the bank result to persist the Payment transition and replay snapshot atomically.

The key reliability rule is that a completed bank/business outcome must not leave the Payment row changed while the public idempotency record remains `in_progress`. If `CompletePaymentCommand` cannot complete the idempotency snapshot, the Payment transition rolls back with it.

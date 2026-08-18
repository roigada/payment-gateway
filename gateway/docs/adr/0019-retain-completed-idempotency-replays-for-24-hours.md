# Retain completed idempotency replays for 24 hours

The gateway guarantees an Idempotency Replay for at least 24 hours after a payment command completes. The 24-hour Idempotency Replay Window is application policy, not deployment configuration.

An application operation deletes only completed idempotency records whose `completed_at` is older than that window. The payment-gateway process owns the periodic, best-effort runner: its ticker, lifecycle, logging, and metrics. Each gateway process runs its own ticker, whose first run occurs after the deployment-configured interval. The interval affects only when records older than the guaranteed window are removed. Cleanup failures are observable and retried on the next run without affecting payment commands.

In-progress claims are never deleted by this operation because they protect recovery of potentially ambiguous Mock Bank side effects through the existing Stuck Idempotency Claim path. Once a completed record is removed, the same Idempotency Key may start a new payment command.

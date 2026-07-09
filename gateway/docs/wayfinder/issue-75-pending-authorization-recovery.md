# Assess Automatic Bank Retry and Pending Authorization Recovery

Research ticket: [Assess automatic bank retry and Pending authorization recovery](https://github.com/roigada/payment-gateway/issues/75)

## Question

Should the roadmap include automatic retry, background recovery, or operator-triggered recovery for Mock Bank timeouts and Pending authorizations, and how should that interact with the existing explicit Authorization Retry endpoint?

## Local Findings

- `Pending` means the Mock Bank authorization outcome is not yet known. It is not a processing state for capture, void, or refund. Source: [gateway/CONTEXT.md](../../CONTEXT.md), [gateway/README.md](../../README.md).
- Initial authorization persists a Pending Payment with an authorization Bank Operation Key before calling the Mock Bank. If the Mock Bank times out or is unavailable, the gateway releases the public idempotency claim and leaves the Payment Pending. Source: [ADR-0012](../adr/0012-do-not-hold-database-transactions-across-bank-calls.md), [gateway/internal/app/payment_service.go](../../internal/app/payment_service.go), [gateway/internal/app/payment_service_test.go](../../internal/app/payment_service_test.go).
- Authorization Retry reuses the Pending Payment's authorization Bank Operation Key, but it still requires the caller to provide card details. Source: [gateway/internal/app/payment_service.go](../../internal/app/payment_service.go), [gateway/README.md](../../README.md).
- The gateway deliberately stores an authorization card fingerprint, not raw card details or CVV. The fingerprint lets the gateway reject retries with a different card, but it cannot be used to make a new Mock Bank authorization request. Source: [ADR-0014](../adr/0014-authorization-retry-fingerprints.md), [gateway/migrations/000001_create_payments.up.sql](../../migrations/000001_create_payments.up.sql).
- The Mock Bank adapter sends the gateway-generated Bank Operation Key as the Mock Bank `Idempotency-Key` for authorization calls. Reusing that key is correct, but the request still needs the authorization body. Source: [gateway/internal/mockbank/client.go](../../internal/mockbank/client.go).
- The separate stale idempotency recovery decision already covers crash-shaped recovery where a public command claim remains `in_progress`; that recovery should reuse stored Bank Operation Keys when the client retries the same command. Source: [Assess idempotency cleanup and stuck in-progress recovery](issue-71-idempotency-cleanup-recovery.md).

## Options Considered

1. **Automatic in-request retry after a Mock Bank timeout**
   - Can improve transient reliability, but only inside the original request while card details are still present.
   - Weak first roadmap item because retries around an unknown side effect must be conservative, bounded, observable, and carefully tested. It also adds less portfolio signal than the already-ranked stale idempotency recovery work.

2. **Background recovery worker for Pending authorizations**
   - Not a good fit for the current gateway. Once the request ends, the gateway no longer has raw card details or CVV, so a worker cannot safely call the Mock Bank without changing the data-sensitivity model.
   - Adding stored card data, payment tokens, or vault integration would redraw the project scope and weaken the current learning-project security posture.

3. **Operator-triggered bank retry**
   - Has the same card-material problem as a background worker if it tries to call the Mock Bank.
   - Operator tooling can still be useful if scoped to inspection: list aging Pending Payments, see last retry/error facts, and follow a runbook that asks the client/order service to submit an Authorization Retry.

4. **Keep explicit Authorization Retry as the authoritative resolver**
   - Fits the existing API and domain language: the client resolves a Pending Payment by submitting the same card details again.
   - Preserves the gateway's current safety boundary: no raw card storage, no background authorization with hidden credentials, and reuse of the original authorization Bank Operation Key.

5. **Pending visibility and runbook support**
   - Adds operational clarity without changing the payment data model.
   - Lower priority than CI, OpenAPI, command-level stale idempotency recovery, packaging, and reviewer docs, but it can pair naturally with the observability/runbook roadmap item.

## Decision

The roadmap should **not prioritize automatic background bank retry for Pending authorizations**. The existing explicit **Authorization Retry** endpoint should remain the only mechanism that resolves a Pending Payment by calling the Mock Bank, because it requires caller-supplied card details while reusing the stored authorization Bank Operation Key.

Recommended roadmap treatment:

1. Keep Pending authorization recovery as **client-driven Authorization Retry**.
2. Do not add a background worker that calls the Mock Bank unless a future effort introduces a deliberate card-token or vault model.
3. Do not add operator-triggered bank retry unless it also has a safe way to receive card details; otherwise it should be inspection/runbook tooling, not a bank-calling command.
4. Allow a small, lower-priority operational slice for Pending visibility: query/filter aging Pending Payments, metrics or alerting for Pending age/count, and a runbook that points operators back to client Authorization Retry.
5. Treat simple bounded retry within the original HTTP request as optional polish, not a top-ranked portfolio feature, and only if it is explicitly tied to Bank Operation Key reuse, low retry counts, timeout budgets, and metrics.

## Implications for Later Specs

- The final ranking should not put Pending background recovery above higher-signal backend work such as CI quality gates, gateway OpenAPI, command-level stale idempotency recovery, production image/release workflow, reviewer guide, or alert/runbook coverage.
- If a later spec includes Pending operations, frame it as operational visibility and reviewer clarity, not as a hidden auto-retry system.
- Tests for any retry polish must prove that retries reuse the original authorization Bank Operation Key and never require storing raw card details.
- A future tokenized-card model would be a new destination, not a small extension of this roadmap.

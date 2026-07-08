# Inventory Current Portfolio Strengths and Gaps

Research ticket: [Inventory current portfolio strengths and gaps](https://github.com/roigada/payment-gateway/issues/73)

## Question

What backend portfolio signals does the current payment-gateway repo already demonstrate, and what visible gaps remain when judged for a first backend developer role?

## Evidence Checked

- Root reviewer surface: [README.md](../../../README.md), [Makefile](../../../Makefile), [compose.yaml](../../../compose.yaml), [demo/smoke.sh](../../../demo/smoke.sh), and [demo/payment-gateway.http](../../../demo/payment-gateway.http).
- Gateway service docs: [gateway/README.md](../../README.md), [gateway/CONTEXT.md](../../CONTEXT.md), and ADRs in [gateway/docs/adr](../adr).
- Gateway implementation: domain, app, HTTP, Postgres, Mock Bank adapter, observability, migrations, and command wiring under [gateway/internal](../../internal) and [gateway/cmd/payment-gateway](../../cmd/payment-gateway).
- Local verification: `go test ./...` from `gateway/` passed on 2026-07-09 across command wiring, app, domain, HTTP, Mock Bank adapter, observability, and Postgres packages.

## Current Strengths

1. **Clear backend domain modeling**
   - The gateway has a maintained glossary for Payment, Payment ID, Payment Status, Idempotency Key, Bank Operation Key, Pending, Authorized, Expired, Captured, Voided, Refunded, and related concepts.
   - The domain model protects lifecycle rules and keeps Mock Bank references private from public API responses.
   - ADRs explain why the API, package layout, error contract, authorization retry fingerprints, expiration behavior, metrics, and persistence boundary work the way they do.

2. **Real payment workflow, not a CRUD-only demo**
   - The public API supports Authorize, Authorization Retry, Capture, Void, Refund, Fetch, and Search Payments.
   - It models meaningful payment outcomes: `pending`, `authorized`, `expired`, `declined`, `captured`, `voided`, and `refunded`.
   - Capture, Void, and Refund are client-driven, full-amount operations with Payment Status conflict handling.

3. **Idempotency and side-effect recovery foundations**
   - Mutating commands require Idempotency Keys and persist replay snapshots for completed commands.
   - The gateway separates public Idempotency Keys from Bank Operation Keys and stores bank recovery facts before outbound Mock Bank calls.
   - ADR-0012 and ADR-0019 document the important reliability constraint: do not hold database transactions across bank calls, and commit Payment transition plus idempotency replay snapshot atomically after a definitive bank result.

4. **Postgres-backed persistence with meaningful constraints**
   - The schema validates Payment ID shape, Currency, Payment Status values, Decline Reason values, idempotency record shape, and status/private-field combinations.
   - The Postgres adapter owns command claims, row locking, Payment Status checks, authorization card fingerprint checks, Bank Operation Key persistence, replay, completion, and release behavior.
   - Integration tests cover persistence of lifecycle transitions, idempotency conflicts, rollback of incomplete completions, expiration refresh, and search behavior.

5. **Operational HTTP behavior**
   - The service exposes `/healthz`, `/readyz`, and `/metrics`.
   - The HTTP layer has stable JSON envelopes, stable public error codes, media-type handling, empty-body enforcement for no-input commands, panic recovery, request logging, and bounded route labels for metrics.
   - Gateway-owned error kinds prevent raw adapter or bank errors from leaking through the app boundary.

6. **Observability already visible in the demo**
   - Custom Prometheus metrics cover HTTP RED, payment operation outcomes, Mock Bank dependency RED, and Postgres pool USE.
   - The root Compose demo starts Prometheus and Grafana with a provisioned Gateway Overview dashboard.
   - ADR-0020 records metric boundaries, label cardinality rules, and the choice not to scrape the bundled Mock Bank directly.

7. **Reviewer-friendly local runtime**
   - `make demo` starts Postgres, migrations, Mock Bank, gateway, Prometheus, and Grafana.
   - `make demo-smoke` waits for readiness, Authorizes a Payment, Captures it, Fetches it, and asserts final Payment Status `captured`.
   - The root README explains the authorship boundary between gateway code and copied Mock Bank demo infrastructure.

8. **Test coverage breadth**
   - The suite covers command configuration, domain invariants, app use cases, HTTP adapter behavior, Mock Bank adapter translation, Prometheus metrics, Postgres integration, and runtime configuration.
   - `go test ./...` passes locally from the gateway module.

## Visible Gaps

1. **No CI quality gate is visible on GitHub**
   - There is no `.github/workflows` directory.
   - A reviewer cannot see automated `go test ./...`, race testing, migration checks, or Compose smoke status on pull requests or commits.
   - This is already addressed by the decision in [Assess CI/CD release and deployment options](https://github.com/roigada/payment-gateway/issues/70): prioritize GitHub Actions quality gates plus Compose smoke.

2. **No production gateway artifact**
   - The demo stack runs the gateway from a source-mounted Go image.
   - There is no authored production gateway `Dockerfile`, published image, or release workflow.
   - This does not weaken the local demo, but it leaves deployability and runtime packaging as an obvious portfolio gap.

3. **Gateway API contract is prose/examples only**
   - The bundled Mock Bank has an OpenAPI contract, but the authored gateway API does not.
   - The README and `.http` collection are useful, yet a reviewer cannot inspect or validate a formal gateway contract.
   - A gateway OpenAPI spec would make the existing API easier to review and could feed contract checks in CI.

4. **Demo explains how to run the system, but not the architecture journey**
   - The root README and gateway README are practical, and ADRs are strong once opened.
   - There is no short reviewer path that says, in one screen, "here is the high-signal backend work and where to inspect it."
   - The docs could better surface idempotency, Bank Operation Key recovery, Postgres transactions, metrics, and the Mock Bank boundary without requiring a reviewer to infer them from code and ADRs.

5. **Idempotency crash recovery is incomplete**
   - The gateway has the right recovery facts, but stale `in_progress` public idempotency rows are not recovered after a process crash.
   - This gap is already resolved directionally by [Assess idempotency cleanup and stuck in-progress recovery](https://github.com/roigada/payment-gateway/issues/71): command-level stale-claim recovery should be prioritized over TTL deletion.

6. **Pending authorization recovery is manual and client-driven**
   - Authorization Retry exists for Pending Payments, but there is not yet a ranked decision on automatic retry, background recovery, or operator-triggered recovery for unresolved Mock Bank authorization outcomes.
   - This remains an open roadmap decision in [Assess automatic bank retry and Pending authorization recovery](https://github.com/roigada/payment-gateway/issues/75).

7. **Observability lacks alerting/runbook layer**
   - Metrics and dashboarding are present.
   - Alert rules, SLOs, runbooks, tracing, and request/log correlation are not yet present.
   - ADR-0020 intentionally left alert rules and Alertmanager out of the first observability slice; the next decision should rank which observability layer adds the best portfolio signal.

8. **Security and public API hardening are intentionally minimal**
   - There is no authentication, authorization, rate limiting, CORS policy, or TLS story in the gateway.
   - For this portfolio demo, these should not outrank reliability, CI, packaging, API contract, or demo clarity unless the reviewer demo surface later requires them.

9. **Search is simple and unpaginated**
   - Search supports order, customer, and status filters, but the public API docs do not advertise pagination, sorting controls, or result limits.
   - This is a lower-signal gap than the reliability and operability items above unless a later spec expands the gateway into a heavier query surface.

## Decision

The repo already demonstrates unusually strong first-backend-role evidence in domain modeling, payment lifecycle behavior, idempotency design, Postgres correctness, adapter boundaries, tests, and local observability. The highest-value roadmap should therefore avoid generic CRUD expansion and instead make the existing strengths more visible, verifiable, and production-shaped.

The strongest remaining gaps to feed into ranking are:

1. **CI plus Compose smoke**: makes existing test and demo quality visible on GitHub.
2. **Command-level stale idempotency recovery**: completes the strongest reliability story already implied by Bank Operation Keys.
3. **Production gateway image/release workflow**: proves deployability without requiring hosted infrastructure first.
4. **Gateway OpenAPI contract plus contract checks**: turns the public API from prose/examples into a reviewable backend contract.
5. **Reviewer architecture/demo guide**: exposes the strongest backend decisions quickly in the first 5-10 minutes.
6. **Observability next layer**: choose alerting/SLO/runbook/tracing/log correlation based on portfolio signal in the observability ticket.
7. **Pending authorization recovery policy**: decide whether automatic, background, or operator-triggered recovery is worth implementing after reliability priorities are ranked.

## Implications for Later Specs

- The ranked roadmap should emphasize proof and production readiness over adding more Payment operations.
- Features should be framed around evidence a hiring reviewer can inspect: CI checks, reproducible runtime packaging, formal API contract, failure recovery tests, dashboard/alerts, and concise architectural docs.
- A full frontend, real payment provider, broad authentication system, hosted deployment, and rich search pagination should remain lower priority unless the reviewer-demo decision shows they materially improve comprehension.

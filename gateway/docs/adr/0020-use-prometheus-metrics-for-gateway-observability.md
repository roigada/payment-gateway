# Use Prometheus Metrics for Gateway Observability

The gateway exposes Prometheus-format metrics from the application through `/metrics` so local and deployed environments can scrape the same operational signal. Go code records bounded, low-cardinality service metrics: gateway HTTP RED metrics, payment operation outcome RED metrics, outbound Mock Bank dependency RED metrics, Postgres client pool USE metrics, and Go runtime/process collectors.

Prometheus and Grafana are local demo/runtime infrastructure. They live at the repository root with the Compose demo configuration and are not gateway application dependencies. The default demo runtime starts Prometheus and Grafana so reviewers can inspect operational behavior without hand-configuring an observability stack.

Prometheus intentionally scrapes only the payment gateway `/metrics` endpoint. It must not scrape the bundled Mock Bank directly, even though the Mock Bank lives in this repository for demo ergonomics. Operationally, the gateway treats the bundled Mock Bank as an External Bank Dependency; the gateway can observe dependency request rate, result, and latency through outbound `mock_bank_*` metrics, but it does not inspect the bank's internals.

Grafana is provisioned with a single Gateway Overview dashboard. Dashboard language should use production-oriented headings such as External Bank Dependency. Dependency error panels classify `timeout`, `unavailable`, and `internal` results as dependency failures; business-like results such as `declined`, `expired`, `state_conflict`, and `invalid_input` remain visible in result breakdowns without being collapsed into outage panels.

Gateway-owned custom metrics use the `payment_gateway_` prefix so their service ownership is clear even outside the local demo Prometheus job labels. Standard Go runtime and process collectors keep their conventional `go_*` and `process_*` names.

Payment operation metrics are recorded at the application command boundary for mutating payment use cases. The gateway exposes `payment_gateway_payment_operations_total{operation,outcome}` and `payment_gateway_payment_operation_duration_seconds{operation,outcome}`. The `operation` label uses gateway command names, and the `outcome` label uses public Payment Status values, PaymentErrorKind values, or `replayed` for Idempotency Replay. Normal payment lifecycle outcomes such as `declined`, `pending`, `expired`, durable Payment Status values, and `replayed` remain visible without being classified as outage/error outcomes. Decline Reason is intentionally not a metric label.

Stuck Idempotency Claim recovery is recorded separately as `payment_gateway_idempotency_recovery_total{operation,result}` so existing payment operation metrics retain their Idempotency Replay semantics. `operation` is limited to gateway command names, while `result` is limited to `attempted`, `recovered`, `unrecoverable`, and `conflict`. The application records `attempted` after the store has identified and reclaimed a same-fingerprint stuck claim; it records `recovered` only after the normal atomic command completion path persists a replay snapshot. Recovery conflicts and missing or inconsistent recovery facts are counted without exposing public Idempotency Keys, Payment IDs, card data, bank identifiers, or raw error text as labels.

Postgres observability is gateway-owned, client-side connection pool metrics from Go's `database/sql` pool. This gives the gateway visibility into pool utilization and saturation without adding a Postgres server exporter or scraping the bundled database directly. The gateway collects these pool values at Prometheus scrape time through a custom collector around `sql.DB.Stats()` instead of maintaining a background polling loop.

The gateway also sets an explicit database connection pool budget so pool saturation metrics have a meaningful ceiling. Local defaults are intentionally conservative and environment-configurable.

Postgres server diagnostics, synthetic load generation, alert rules, and Alertmanager remain out of scope. The dashboard should keep payment operation outcomes separate from HTTP status codes and Mock Bank dependency results.

# Use Prometheus Metrics for Gateway Observability

The gateway exposes Prometheus-format metrics from the application through `/metrics` so local and deployed environments can scrape the same operational signal. Go code records bounded, low-cardinality service metrics: gateway HTTP RED metrics, outbound Mock Bank dependency RED metrics, and Go runtime/process collectors.

Prometheus and Grafana are local demo/runtime infrastructure. They live at the repository root with the Compose demo configuration and are not gateway application dependencies. The default demo runtime starts Prometheus and Grafana so reviewers can inspect operational behavior without hand-configuring an observability stack.

Prometheus intentionally scrapes only the payment gateway `/metrics` endpoint. It must not scrape the bundled Mock Bank directly, even though the Mock Bank lives in this repository for demo ergonomics. Operationally, the gateway treats the bundled Mock Bank as an External Bank Dependency; the gateway can observe dependency request rate, result, and latency through outbound `mock_bank_*` metrics, but it does not inspect the bank's internals.

Grafana is provisioned with a single Gateway Overview dashboard. Dashboard language should use production-oriented headings such as External Bank Dependency while keeping the existing metric names stable. Dependency error panels classify `timeout`, `unavailable`, and `internal` results as dependency failures; business-like results such as `declined`, `expired`, `state_conflict`, and `invalid_input` remain visible in result breakdowns without being collapsed into outage panels.

Postgres diagnostics, database pool metrics, payment operation outcome metrics, synthetic load generation, alert rules, and Alertmanager are deferred to later slices. The current dashboard should not infer payment outcome observability from HTTP status codes or Mock Bank results.

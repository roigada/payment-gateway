# Use Prometheus Metrics for Gateway Observability

The gateway exposes Prometheus-format metrics from the application through `/metrics` so local and deployed environments can scrape the same operational signal. Go code records bounded, low-cardinality service metrics: gateway HTTP RED metrics, outbound Mock Bank dependency RED metrics, and Go runtime/process collectors.

Prometheus and Grafana are local demo/runtime infrastructure. They live at the repository root with the Compose demo configuration and are not gateway application dependencies. The default demo runtime starts Prometheus and Grafana so reviewers can inspect operational behavior without hand-configuring an observability stack.

Prometheus intentionally scrapes only the payment gateway `/metrics` endpoint. It must not scrape the bundled Mock Bank directly, even though the Mock Bank lives in this repository for demo ergonomics. Operationally, the gateway treats the bundled Mock Bank as an External Bank Dependency; the gateway can observe dependency request rate, result, and latency through outbound `mock_bank_*` metrics, but it does not inspect the bank's internals.

Grafana is provisioned with a single Gateway Overview dashboard. Dashboard language should use production-oriented headings such as External Bank Dependency. Dependency error panels classify `timeout`, `unavailable`, and `internal` results as dependency failures; business-like results such as `declined`, `expired`, `state_conflict`, and `invalid_input` remain visible in result breakdowns without being collapsed into outage panels.

Gateway-owned custom metrics use the `payment_gateway_` prefix so their service ownership is clear even outside the local demo Prometheus job labels. Standard Go runtime and process collectors keep their conventional `go_*` and `process_*` names.

Postgres observability is gateway-owned, client-side connection pool metrics from Go's `database/sql` pool. This gives the gateway visibility into pool utilization and saturation without adding a Postgres server exporter or scraping the bundled database directly. The gateway collects these pool values at Prometheus scrape time through a custom collector around `sql.DB.Stats()` instead of maintaining a background polling loop.

The gateway also sets an explicit database connection pool budget so pool saturation metrics have a meaningful ceiling. Local defaults are intentionally conservative and environment-configurable.

Postgres server diagnostics, payment operation outcome metrics, synthetic load generation, alert rules, and Alertmanager are deferred to later slices. The current dashboard should not infer payment outcome observability from HTTP status codes or Mock Bank results.

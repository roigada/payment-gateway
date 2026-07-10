# Gateway Alert Runbook

These alerts are Prometheus-first local demo alerts for gateway operational symptoms. Prometheus loads the rules from `observability/prometheus/rules/gateway-alerts.yml`; Grafana remains an inspection surface through the Prometheus datasource.

Validate rule loading and syntax from the repository root:

```sh
make observability-check
```

Start the local demo stack:

```sh
make demo
```

Then inspect:

- Prometheus targets, rules, alerts, and ad hoc PromQL at `http://localhost:9090`.
- Grafana Gateway Overview dashboard at `http://localhost:3000` with `admin` / `payment-gateway`.
- Gateway metrics directly at `http://localhost:8080/metrics`.

Run demo traffic with:

```sh
make demo-smoke
```

## Gateway Elevated 5xx Responses

Alert: `GatewayElevated5xxResponses`

PromQL:

```promql
sum(rate(payment_gateway_http_server_requests_total{code=~"5.."}[5m])) > 0.05
```

This alert detects sustained gateway HTTP 5xx responses. Likely causes include handler panics recovered by middleware, database failures surfaced as internal errors, unexpected JSON/write failures, or other gateway defects that prevent a request from completing normally.

Inspect in Prometheus:

```promql
sum by (method, route, code) (rate(payment_gateway_http_server_requests_total{code=~"5.."}[5m]))
```

Inspect in Grafana on the Gateway Overview HTTP request panels. Break down by `route` and `code` first, then correlate with gateway logs for the same route and time window.

Local reproduction can use the demo stack plus request traffic that triggers a server-side failure. For example, stop or pause required infrastructure such as the gateway Postgres container, send gateway API requests, and watch the 5xx series in Prometheus after the next scrape. Restore the dependency before continuing normal smoke checks.

## Mock Bank Dependency Failures

Alert: `GatewayMockBankDependencyFailures`

PromQL:

```promql
sum(rate(payment_gateway_mock_bank_requests_total{result=~"timeout|unavailable|internal"}[5m])) > 0.02
```

This alert detects aggregate outbound Mock Bank dependency failures through gateway-owned dependency metrics. It intentionally counts only `timeout`, `unavailable`, and `internal` results. Business-like bank results such as `declined`, `expired`, `state_conflict`, and `invalid_input` remain visible but are not dependency outage symptoms.

Inspect the aggregate signal in Prometheus:

```promql
sum(rate(payment_gateway_mock_bank_requests_total{result=~"timeout|unavailable|internal"}[5m]))
```

Break down by Mock Bank operation during diagnosis:

```promql
sum by (operation, result) (rate(payment_gateway_mock_bank_requests_total{result=~"timeout|unavailable|internal"}[5m]))
```

The `operation` label is one of `authorize`, `capture`, `void`, and `refund`. Use the Grafana External Bank Dependency panels to compare dependency failure rate with request volume and latency.

Local reproduction can use the demo stack and Mock Bank chaos settings. Increase Mock Bank failure behavior in Compose or temporarily stop the `mock-bank` service, then run authorization/capture/void/refund traffic through the gateway. The gateway should record `unavailable`, `timeout`, or `internal` dependency results from its outbound adapter without scraping Mock Bank directly.

## Payment Operation Technical Failures

Alert: `GatewayPaymentOperationTechnicalFailures`

PromQL:

```promql
sum(rate(payment_gateway_payment_operations_total{outcome=~"bank_timeout|bank_unavailable|internal"}[5m])) > 0.02
```

This alert detects gateway payment commands that end in technical failure outcomes. It counts only `bank_timeout`, `bank_unavailable`, and `internal`. It must not count normal payment lifecycle or caller outcomes such as `declined`, `pending`, `expired`, `payment_status_conflict`, `invalid_input`, `idempotency_conflict`, or `bank_state_conflict`.

Inspect in Prometheus:

```promql
sum by (operation, outcome) (rate(payment_gateway_payment_operations_total[5m]))
```

For the alerting subset:

```promql
sum by (operation, outcome) (rate(payment_gateway_payment_operations_total{outcome=~"bank_timeout|bank_unavailable|internal"}[5m]))
```

The `operation` label uses gateway command names such as `authorize_payment`, `retry_authorization`, `capture_payment`, `void_payment`, and `refund_payment`. Use Grafana Payment Operations panels to compare total operation volume, technical failures, normal statuses, and replayed idempotency responses.

Local reproduction follows the same path as dependency failures: run the demo stack, make the Mock Bank unavailable or slow enough to fail gateway calls, then send payment command traffic. Confirm whether the technical operation failures line up with dependency failures; if they do not, investigate gateway persistence, validation, and command handling separately.

## Postgres Pool Wait Growth

Alert: `GatewayPostgresPoolWaitGrowth`

PromQL:

```promql
rate(payment_gateway_postgres_pool_wait_count_total[5m]) > 0 and rate(payment_gateway_postgres_pool_wait_duration_seconds_total[5m]) > 0
```

This alert detects sustained connection pool wait growth. High pool utilization is useful context, but this alert requires wait count and wait duration growth so it pages on actual pool saturation symptoms instead of utilization alone.

Inspect in Prometheus:

```promql
rate(payment_gateway_postgres_pool_wait_count_total[5m])
rate(payment_gateway_postgres_pool_wait_duration_seconds_total[5m])
payment_gateway_postgres_pool_in_use_connections / payment_gateway_postgres_pool_max_open_connections
```

Inspect in Grafana on the Postgres pool panels. Compare wait growth with open, in-use, idle, and max-open connection series.

Likely causes include concurrent gateway requests exceeding the configured pool budget, slow database queries, blocked transactions, or database connectivity degradation. The local demo gateway defaults `DATABASE_MAX_OPEN_CONNECTIONS` to `10`; lowering that value in Compose while running concurrent API traffic can make pool waits easier to reproduce locally.

## Business Outcomes Are Not Outages

Declined, Pending, and Expired Payments are part of the normal Payment lifecycle. Caller and state outcomes such as `payment_status_conflict`, `invalid_input`, `idempotency_conflict`, and `bank_state_conflict` are also not classified as outages in this first alert slice.

During diagnosis, keep these categories separate:

- Payment lifecycle outcomes: `declined`, `pending`, `expired`, `authorized`, `captured`, `voided`, `refunded`.
- Caller or domain outcomes: `invalid_input`, `payment_status_conflict`, `idempotency_conflict`, `idempotency_in_progress`, `bank_state_conflict`, `not_found`.
- Technical outage-like outcomes: `bank_timeout`, `bank_unavailable`, `internal`.

The alerts in this runbook only fire on HTTP 5xx, Mock Bank dependency `timeout|unavailable|internal`, Payment operation `bank_timeout|bank_unavailable|internal`, and Postgres pool wait growth.

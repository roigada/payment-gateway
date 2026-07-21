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
- Gateway metrics through the Prometheus target. The gateway's `:9091` operational listener remains private to the Compose network and is not published to the host.

Run demo traffic with:

```sh
make demo-smoke
```

## Payment API Rate Limits

Rate-limit rejections are expected admission-control outcomes, not an alerting condition. Inspect the Gateway Overview panel or query Prometheus by the bounded route class:

```promql
sum by (route_class) (rate(payment_gateway_rate_limit_rejections_total[1m]))
```

The only route classes are `read` and `write`; the metric never includes a Service Principal, Service Credential, Payment ID, Idempotency Key, or raw URI. If the rejection rate is unexpected, check the configured `RATE_LIMIT_*` quotas and the Order Service's request pattern. A rejected caller receives `429 Too Many Requests` with a whole-second `Retry-After` header and can retry after that delay. There is intentionally no alert until production traffic establishes a useful threshold.

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

## Aging Pending Payments

Alert: `GatewayAgingPendingPayments`

PromQL:

```promql
payment_gateway_oldest_pending_payment_age_seconds > 300
```

This is an `info`-severity local/demo operational-visibility alert, lower severity than the gateway outage alerts. It means at least one current Pending Payment has been unresolved for more than five minutes. The aggregate metrics deliberately contain no Payment, Order, Customer, Mock Bank, fingerprint, card-number, or CVV labels:

```promql
payment_gateway_pending_payments_total
payment_gateway_oldest_pending_payment_age_seconds
```

`Pending` means the Mock Bank authorization outcome is not yet known. It does not mean the Payment failed, is processing capture, or needs void or refund work. This alert and the aggregate metrics do not resolve a Payment or change its status.

### Inspect concrete Pending Payments locally

After the aggregate alert indicates an Aging Pending Payment, inspect the local gateway Postgres database. From the repository root while the demo stack is running:

```sh
docker compose exec postgres psql -U payment_gateway -d payment_gateway -c "
  SELECT id, order_id, customer_id, created_at, now() - created_at AS pending_age
  FROM payments
  WHERE status = 'pending'
  ORDER BY created_at ASC;"
```

Use this local inspection path only after the aggregate metric has identified the symptom. Do not add Payment, Order, Customer, Bank Operation Key, Authorization Request Fingerprint, Authorization Card Fingerprint, card number, or CVV as Prometheus labels.

### Resolve with the client/order service

Pending resolution is client-driven through Authorization Retry. Ask the client or order service to submit Authorization Retry with the card details when resolution is required; there is no background worker that calls the Mock Bank for this alert.

Authorization Retry reuses the Payment's stored authorization Bank Operation Key, so repeated bank calls refer to the same authorization operation. The stored Authorization Card Fingerprint checks that the retry uses the same card number and expiry as the original authorization. It is a non-reversible check, not raw card storage; it excludes CVV and cannot be used by a background worker to call the Mock Bank.

## Business Outcomes Are Not Outages

Declined, Pending, and Expired Payments are part of the normal Payment lifecycle. Caller and state outcomes such as `payment_status_conflict`, `invalid_input`, `idempotency_conflict`, and `bank_state_conflict` are also not classified as outages in this first alert slice.

During diagnosis, keep these categories separate:

- Payment lifecycle outcomes: `declined`, `pending`, `expired`, `authorized`, `captured`, `voided`, `refunded`.
- Caller or domain outcomes: `invalid_input`, `payment_status_conflict`, `idempotency_conflict`, `idempotency_in_progress`, `bank_state_conflict`, `not_found`.
- Technical outage-like outcomes: `bank_timeout`, `bank_unavailable`, `internal`.

The warning-severity alerts in this runbook fire on HTTP 5xx, Mock Bank dependency `timeout|unavailable|internal`, Payment operation `bank_timeout|bank_unavailable|internal`, and Postgres pool wait growth. Aging Pending Payment visibility is a separate `info`-severity alert and is not an outage signal.

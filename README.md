# Payment Gateway Demo

This repository is a portfolio demo of a payment gateway between an e-commerce Order Service and a Mock Bank. The **gateway service in [`gateway/`](gateway/) is authored project work**: it owns Payment records, retry safety, recovery, and the translation between public Payment IDs and bank references. [`mock-bank/`](mock-bank/) is **bundled third-party demo dependency infrastructure**, included so the complete system can run locally; see its [provenance](mock-bank/PROVENANCE.md).

## Reviewer path (5–10 minutes)

From a clean checkout with Docker available, follow this path in order. It takes you from a running system, through its public behavior and operational signals, to the implementation decisions behind them.

1. Start the local demo runtime:

   ```sh
   make demo
   ```

   This starts Postgres, applies gateway migrations, starts the bundled Mock Bank, runs the gateway on `http://localhost:8080`, and provisions Prometheus and Grafana.

2. In another terminal, verify the public happy path:

   ```sh
   make demo-smoke
   ```

   The smoke check waits for readiness, authorizes a Payment through the Mock Bank, captures it, fetches it, and verifies the final `captured` status.

3. Explore the API yourself with the runnable [`demo/payment-gateway.http`](demo/payment-gateway.http) collection. It includes health, readiness, metrics, authorization, an Idempotency Replay, capture, fetch, search, and a declined payment. The formal endpoint and schema reference is the gateway [OpenAPI contract](gateway/docs/api/openapi.yaml).

4. Inspect the traffic in [Grafana](http://localhost:3000) (`admin` / `payment-gateway`). The provisioned Gateway Overview dashboard refreshes every five seconds; run the smoke command again if it has no traffic yet.

5. Inspect the same gateway-owned signals in [Prometheus](http://localhost:9090): check the scrape target, then query the HTTP, payment-operation, Mock Bank dependency, and Postgres pool metrics. Prometheus intentionally scrapes the gateway’s `/metrics`, not the bundled Mock Bank.

6. Go deeper through the [gateway README](gateway/README.md), [architecture decisions](gateway/docs/adr/), [domain language](gateway/CONTEXT.md), and [alert runbook](gateway/docs/runbooks/gateway-alerts.md).

## What the gateway demonstrates

- **Safe client retries:** callers supply an **Idempotency Key** for every mutating command. Repeating the same key and request replays the original result; reusing it with different values returns `409 Conflict` rather than creating another payment operation.
- **No duplicate bank side effects:** the gateway persists a separate **Bank Operation Key** before calling the Mock Bank. A gateway retry can therefore ask the bank to resume one operation instead of capture, void, refund, or authorize twice.
- **Durable recovery:** Postgres persists the Payment command claim and recovery facts before the bank call, then persists the Payment transition and replay response atomically after a definitive result. That keeps recoverable failures from becoming silent or duplicate side effects.
- **Operational visibility:** Prometheus metrics expose HTTP, payment-command, Mock Bank dependency, and Postgres pool behavior with bounded labels; Grafana makes the signal inspectable in the local demo. The [runbook](gateway/docs/runbooks/gateway-alerts.md) explains the currently provisioned alert symptoms and queries.

## Local runtime reference

Useful endpoints after `make demo`:

```text
Gateway health:       http://localhost:8080/healthz
Gateway readiness:    http://localhost:8080/readyz
Gateway metrics:      http://localhost:8080/metrics
Gateway API contract: gateway/docs/api/openapi.yaml
Grafana:              http://localhost:3000
Prometheus:           http://localhost:9090
Mock Bank docs:       http://localhost:8787/docs
Mock Bank health:     http://localhost:8787/health
```

If default host ports are already in use:

```sh
GRAFANA_PORT=3001 PROMETHEUS_PORT=9091 MOCK_BANK_PORT=8788 make demo
```

Stop or reset the local runtime with:

```sh
make demo-down
make demo-reset
```

Set `MOCK_BANK_BASE_URL=http://127.0.0.1:8787` when running `make demo-smoke` if you also want the smoke check to assert Mock Bank health.

## Development checks

```sh
make test
make validate-openapi
make observability-check
```

## Repository map

```text
gateway/        Authored gateway implementation, domain glossary, ADRs, migrations, and service docs
mock-bank/      Bundled third-party Mock Bank demo infrastructure
demo/           Demo smoke check and manually runnable HTTP requests
observability/  Local Prometheus and Grafana demo configuration
```

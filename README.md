# Payment Gateway Demo

This repository is a portfolio demo system for a payment gateway that sits between an e-commerce order service and a Mock Bank. The gateway owns Payment records, idempotency, and the translation between public Payment IDs and Mock Bank operation references.

The repository root is the demo surface. The gateway implementation lives in [`gateway/`](gateway/), and the bundled Mock Bank demo dependency lives in [`mock-bank/`](mock-bank/).

## Run

Start the local runtime:

```sh
make demo
```

This starts Postgres, applies gateway migrations, starts the bundled Mock Bank, runs the gateway API on `http://localhost:8080`, and starts the local Prometheus + Grafana observability stack.

Useful endpoints:

```text
Gateway health:       http://localhost:8080/healthz
Gateway readiness:    http://localhost:8080/readyz
Gateway metrics:      http://localhost:8080/metrics
Grafana:              http://localhost:3000
Prometheus:           http://localhost:9090
Mock Bank docs:       http://localhost:8787/docs
Mock Bank health:     http://localhost:8787/health
```

Grafana demo credentials:

```text
Username: admin
Password: payment-gateway
```

If default host ports are already in use, override them when starting the demo:

```sh
GRAFANA_PORT=3001 PROMETHEUS_PORT=9091 MOCK_BANK_PORT=8788 make demo
```

Stop the runtime:

```sh
make demo-down
```

Reset local Compose data:

```sh
make demo-reset
```

Verify the running demo stack through the public gateway API:

```sh
make demo-smoke
```

The smoke check waits for gateway readiness, Authorizes a Payment through the Mock Bank, Captures it, fetches it, and asserts that the final Payment Status is `captured`.

To inspect observability, start the demo stack, open Grafana at `http://localhost:3000`, then run `make demo-smoke` in another terminal. The provisioned Gateway Overview dashboard refreshes every 5 seconds and should populate within a short scrape interval after smoke traffic runs. Prometheus scrapes the gateway `/metrics` endpoint only; it does not scrape the bundled Mock Bank directly. Use the Prometheus UI at `http://localhost:9090` to inspect scrape targets or raw metric queries.

For manual exploration, use [`demo/payment-gateway.http`](demo/payment-gateway.http) with an editor or REST client that supports `.http` request collections.

## Test

Run the gateway test suite:

```sh
make test
```

## Repository Map

```text
gateway/        Gateway implementation, domain glossary, ADRs, migrations, and service docs
mock-bank/      Bundled third-party Mock Bank demo infrastructure copied from benx421/payment-gateway
demo/           Demo smoke check and manually runnable HTTP requests
observability/  Local Prometheus and Grafana demo configuration
```

For gateway API details, payment lifecycle rules, configuration, and observability notes, see [`gateway/README.md`](gateway/README.md).

The Mock Bank is copied dependency code, not authored gateway implementation. Its provenance is documented in [`mock-bank/PROVENANCE.md`](mock-bank/PROVENANCE.md).

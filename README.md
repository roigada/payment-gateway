# Payment Gateway Demo

This repository is a portfolio demo system for a payment gateway that sits between an e-commerce order service and a Mock Bank. The gateway owns Payment records, idempotency, and the translation between public Payment IDs and Mock Bank operation references.

The repository root is the demo surface. The gateway implementation lives in [`gateway/`](gateway/), and the bundled Mock Bank demo dependency will live in `mock-bank/`.

## Run

Start the local runtime:

```sh
make demo
```

This currently starts Postgres, applies gateway migrations, and runs the gateway API on `http://localhost:8080`. The Mock Bank is still an external dependency in this slice and will be bundled under `mock-bank/` in the next demo slice.

Useful endpoints:

```text
Gateway health:    http://localhost:8080/healthz
Gateway readiness: http://localhost:8080/readyz
Gateway metrics:   http://localhost:8080/metrics
```

Stop the runtime:

```sh
make demo-down
```

Reset local Compose data:

```sh
make demo-reset
```

## Test

Run the gateway test suite:

```sh
make test
```

## Repository Map

```text
gateway/    Gateway implementation, domain glossary, ADRs, migrations, and service docs
mock-bank/  Bundled third-party Mock Bank demo infrastructure, copied as-is in a later slice
demo/       Demo smoke test and manually runnable HTTP requests, added in a later slice
```

For gateway API details, payment lifecycle rules, configuration, and observability notes, see [`gateway/README.md`](gateway/README.md).

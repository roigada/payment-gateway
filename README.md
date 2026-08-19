# Payment Gateway

A Go HTTP service that sits between an e-commerce Order Service and a card-issuing bank. It
owns Payment records and their lifecycle, enforces idempotency at its own public API, and
translates between gateway-owned Payment IDs and the bank's operation references.

The bank it integrates with fails on purpose — random 500s, latency up to two seconds, and
strict state validation. The gateway's job is to stay correct anyway.

```mermaid
flowchart LR
    OS((Order Service)) -->|"Bearer credential<br/>Idempotency-Key"| API

    subgraph gw["Payment Gateway (gateway/)"]
        API[HTTP API] --> APP[Use cases]
        APP --> DOM[Domain model]
        APP --> PG[(Postgres)]
        APP --> BC[Bank client]
    end

    BC -->|"HTTP + Idempotency-Key<br/>retry on unknown outcome"| BANK[Mock Bank]
```

## What it does

- **Payment lifecycle** — `pending`, `authorized`, `expired`, `declined`, `captured`,
  `voided`, `refunded`, with invalid transitions rejected by the domain model rather than by
  the database or the bank.
- **Idempotency** — mutating endpoints require an `Idempotency-Key`. The same key with the
  same request fingerprint replays the original response; a different fingerprint returns
  `409 Conflict`.
- **Unknown bank outcomes** — when a bank call fails without a definitive answer, the Payment
  stays `pending` and stores the bank operation key, so a retry is a safe re-attempt rather
  than a second charge.
- **Hexagonal structure** — the dependency direction is inward. Adapters (HTTP, Postgres,
  bank client) depend on the application and domain; the domain depends on none of them.
- **Operations** — Prometheus metrics, health and readiness endpoints, service credentials
  with read/write Payment scopes, and a background cleanup worker for completed idempotency
  replays.

**[`gateway/README.md`](gateway/README.md) is the full reference** — architecture, the
payment model, every environment variable, and the complete API with request and response
examples.

## Run the local stack

```sh
make up
```

This builds and starts the Payment Gateway, its Postgres database and migrations, the Mock
Bank and its Postgres database and migrations, Prometheus, and Grafana. The stack is
additive: it does not change or use the standalone setup in `mock-bank/`.

| Service | Address |
| --- | --- |
| Payment Gateway API | <http://localhost:8080> |
| Payment Gateway metrics | <http://localhost:9091/metrics> |
| Mock Bank API and docs | <http://localhost:8787> |
| Prometheus | <http://localhost:9090> |
| Grafana | <http://localhost:3000> (`admin` / `admin`) |

The root stack configures the Mock Bank with a `0.10` failure rate, so approximately 10% of
requests intentionally receive a 500 response. This is for exercising the gateway's retry and
recovery behavior.

### Calling the gateway

The local stack includes one intentionally fake, development-only Order Service credential.
It grants both read and write scopes:

```sh
curl -i \
  -H 'Authorization: Bearer local-order-service-token' \
  http://localhost:8080/api/v1/payments
```

It is not a production secret. Production credentials and their gateway configuration must be
supplied outside this Compose setup.

### Commands

```sh
make up       # build and start in the background
make ps       # show service status
make logs     # stream all service logs
make down     # stop containers, preserving local data
make reset    # delete stack containers, network, and volumes; then start clean
```

`make reset` affects only resources created by this root Compose project. It does not touch
the standalone Mock Bank stack or its files.

### Tests

```sh
cd gateway && go test ./...           # includes Docker-backed integration tests
cd gateway && go test -short ./...    # unit tests only
```

## Repository layout

```text
gateway/     the Payment Gateway service -- the work in this repository
mock-bank/   third-party Mock Bank, bundled as demo infrastructure
observability/  Prometheus and Grafana configuration for the local stack
compose.yaml    the root local stack
```

## Mock Bank attribution

The Mock Bank under [`mock-bank/`](mock-bank/README.md) is **not** my work. It is third-party
code from [github.com/benx421/payment-gateway](https://github.com/benx421/payment-gateway),
vendored here so that `make up` brings the whole environment up in one command. It carries no
license of its own; see [`mock-bank/PROVENANCE.md`](mock-bank/PROVENANCE.md).

Everything under `gateway/`, plus the root stack and observability configuration, is my own
work and is covered by [LICENSE](LICENSE).

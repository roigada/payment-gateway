# Payment Gateway

Payment Gateway is a Go HTTP service that sits between an e-commerce Order Service and a Mock Bank. It owns Payment records, public idempotency, and the translation between gateway-owned Payment IDs and Mock Bank operation references.

The public API authorizes, retries unknown authorization outcomes, captures, voids, refunds, fetches, and searches Payments. Amounts are expressed in cents, Currency is always `USD`, and successful responses use `payment` or `payments` envelopes.

## Structure

```text
cmd/payment-gateway application wiring and HTTP server startup
internal/domain      domain model and invariants
internal/app         use cases, ports, and application-owned time
internal/httpapi     JSON HTTP adapter
internal/postgres    Postgres adapter
internal/uuidgen     UUID-backed ID generator adapter
migrations           plain SQL database migrations
```

The dependency direction is inward: adapters depend on the application and domain, while the domain does not depend on HTTP, Postgres, UUID libraries, or JSON.

## Payment Model

- A Payment has one gateway-owned Payment ID in the `pay_<uuid>` form.
- A Payment belongs to one external `order_id` and one external `customer_id`.
- A Payment has one `amount` in cents and the fixed `USD` currency.
- Public statuses are `pending`, `authorized`, `expired`, `declined`, `captured`, `voided`, and `refunded`.
- `pending` only means the Mock Bank authorization outcome is unknown. It is not used for capture, void, or refund processing.
- `expired` means an approved authorization can no longer be captured or voided. Authorized responses include `authorization_expires_at`.
- Capture, Void, and Refund are client-driven operations and always apply to the full Payment Amount. Partial capture, partial void, and partial refund are not supported.
- Bank References, including Mock Bank authorization, capture, void, refund, and operation keys, are stored internally so the gateway can continue provider communication and recover retries. They are never returned in public API responses.

## Requirements

- Go 1.26.1 or newer
- Postgres
- A Mock Bank service reachable through `MOCK_BANK_BASE_URL`
- A tool for applying plain SQL migrations, such as `migrate`
- Docker, if using the local Compose environment

## Configuration

The service reads configuration from environment variables:

```text
DATABASE_URL                      required Postgres connection string
DATABASE_MAX_OPEN_CONNECTIONS     optional Postgres pool max open connections, defaults to 10
DATABASE_MAX_IDLE_CONNECTIONS     optional Postgres pool max idle connections, defaults to 5
DATABASE_CONNECTION_MAX_LIFETIME  optional Postgres pool connection max lifetime, defaults to 30m
IDEMPOTENCY_CLAIM_STUCK_AFTER     optional stuck idempotency claim threshold, defaults to 5m
MOCK_BANK_BASE_URL                required Mock Bank base URL
FINGERPRINT_SECRET                 required HMAC secret for request and authorization card fingerprints
ADDR                              optional HTTP listen address, defaults to :8080
```

Example:

```sh
export DATABASE_URL='postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable'
export DATABASE_MAX_OPEN_CONNECTIONS='10'
export DATABASE_MAX_IDLE_CONNECTIONS='5'
export DATABASE_CONNECTION_MAX_LIFETIME='30m'
export IDEMPOTENCY_CLAIM_STUCK_AFTER='5m'
export MOCK_BANK_BASE_URL='http://localhost:9090'
export FINGERPRINT_SECRET='local-development-secret'
export ADDR=':8080'
```

The service validates that the Mock Bank base URL is configured and absolute. Mock Bank unavailability does not prevent startup, but payment commands that need the bank will return gateway-owned bank error responses while it is unavailable.

## Database

Apply pending migrations before starting the API:

```sh
migrate -path migrations -database "$DATABASE_URL" up
```

Migration files use the `000001_name.up.sql` and `000001_name.down.sql` naming convention. The application does not run migrations itself.

## Run

```sh
go run ./cmd/payment-gateway
```

## Run With Docker Compose

From the repository root, start Postgres, apply migrations, and run the API for local development:

```sh
docker compose up
```

The root Compose environment starts the gateway API on `http://localhost:8080` and the bundled Mock Bank on the Compose network with:

```text
ADDR=:8080
DATABASE_URL=postgres://payment_gateway:payment_gateway@postgres:5432/payment_gateway?sslmode=disable
DATABASE_MAX_OPEN_CONNECTIONS=10
DATABASE_MAX_IDLE_CONNECTIONS=5
DATABASE_CONNECTION_MAX_LIFETIME=30m
IDEMPOTENCY_CLAIM_STUCK_AFTER=5m
MOCK_BANK_BASE_URL=http://mock-bank:9090
FINGERPRINT_SECRET=local-development-secret
```

The bundled Mock Bank documentation is exposed on `http://localhost:8787/docs` when the root demo stack is running. For standalone gateway development outside root Compose, set `MOCK_BANK_BASE_URL` to a reachable Mock Bank URL.

## Operational Endpoints

Process health does not check Postgres or the Mock Bank:

```sh
curl -i http://localhost:8080/healthz
```

Readiness checks Postgres and does not require Mock Bank availability:

```sh
curl -i http://localhost:8080/readyz
```

Prometheus-format metrics are exposed on the same server:

```sh
curl -i http://localhost:8080/metrics
```

Custom metrics include HTTP RED:

```text
payment_gateway_http_server_requests_total{method,route,code}
payment_gateway_http_server_request_duration_seconds{method,route,code}
```

payment operation outcome RED:

```text
payment_gateway_payment_operations_total{operation,outcome}
payment_gateway_payment_operation_duration_seconds{operation,outcome}
```

and Stuck Idempotency Claim recovery outcomes:

```text
payment_gateway_idempotency_recovery_total{operation,result}
```

`operation` is one of `authorize_payment`, `retry_authorization`, `capture_payment`, `void_payment`, or `refund_payment`; `result` is one of `attempted`, `recovered`, `unrecoverable`, or `conflict`. The metric never labels public Idempotency Keys, Payment IDs, card data, bank IDs, or raw errors.

and Mock Bank dependency RED:

```text
payment_gateway_mock_bank_requests_total{operation,result}
payment_gateway_mock_bank_request_duration_seconds{operation,result}
```

and Postgres client pool USE:

```text
payment_gateway_postgres_pool_open_connections
payment_gateway_postgres_pool_in_use_connections
payment_gateway_postgres_pool_idle_connections
payment_gateway_postgres_pool_max_open_connections
payment_gateway_postgres_pool_wait_count_total
payment_gateway_postgres_pool_wait_duration_seconds_total
payment_gateway_postgres_pool_max_idle_closed_total
payment_gateway_postgres_pool_max_lifetime_closed_total
payment_gateway_postgres_pool_max_idle_time_closed_total
```

Payment operation `operation` labels use gateway command names: `authorize_payment`, `retry_authorization`, `capture_payment`, `void_payment`, and `refund_payment`. Payment operation `outcome` labels use gateway-owned outcomes: public Payment Status values such as `authorized`, `declined`, `pending`, `expired`, `captured`, `voided`, `refunded`; PaymentErrorKind values such as `invalid_input`, `not_found`, `idempotency_conflict`, `idempotency_in_progress`, `payment_status_conflict`, `bank_state_conflict`, `bank_unavailable`, `bank_timeout`, and `internal`; or `replayed` for an Idempotency Replay.

Mock Bank `operation` labels use gateway domain verbs: `authorize`, `capture`, `void`, and `refund`. Mock Bank `result` labels are bounded gateway-facing outcomes: `success`, `declined`, `expired`, `state_conflict`, `invalid_input`, `timeout`, `unavailable`, and `internal`. Dependency-health errors are primarily `timeout` and `unavailable`; `internal` indicates a gateway adapter failure before a usable bank response.

Route labels use bounded route patterns, such as `/v1/payments/{id}`, and metric labels never include Payment IDs, Bank Authorization IDs, Bank Capture IDs, Bank Refund IDs, Order IDs, Customer IDs, Idempotency Keys, card data, Decline Reasons, or raw request URIs. The registry also includes Go runtime and process metrics.

## Public API

Mutating endpoints require an `Idempotency-Key` header. Reusing the same key for the same operation and same request fingerprint replays the original response snapshot. Reusing it with different request values returns `409 Conflict`.

The formal OpenAPI contract is published at [`docs/api/openapi.yaml`](docs/api/openapi.yaml). The runnable request collection in [`../demo/payment-gateway.http`](../demo/payment-gateway.http) remains the companion artifact for manual exploration against a running demo stack.

### Authorize a Payment

```sh
curl -i http://localhost:8080/v1/payments \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: authorize-order-1001' \
  -d '{
    "order_id": "order-1001",
    "customer_id": "customer-501",
    "amount": 1299,
    "card": {
      "number": "4111111111111111",
      "cvv": "123",
      "expiry_month": 12,
      "expiry_year": 2030
    }
  }'
```

Authorized response:

```json
{
  "payment": {
    "id": "pay_550e8400-e29b-41d4-a716-446655440000",
    "order_id": "order-1001",
    "customer_id": "customer-501",
    "amount": 1299,
    "currency": "USD",
    "status": "authorized",
    "authorization_expires_at": "2026-06-18T13:00:00Z",
    "created_at": "2026-06-18T12:00:00Z",
    "updated_at": "2026-06-18T12:00:00Z"
  }
}
```

Authorization can also create a `declined` Payment with `decline_reason`, or a `pending` Payment when the Mock Bank authorization outcome is unknown:

```json
{
  "payment": {
    "id": "pay_550e8400-e29b-41d4-a716-446655440000",
    "order_id": "order-1001",
    "customer_id": "customer-501",
    "amount": 1299,
    "currency": "USD",
    "status": "pending",
    "created_at": "2026-06-18T12:00:00Z",
    "updated_at": "2026-06-18T12:00:00Z"
  }
}
```

### Retry a Pending Authorization

Authorization retry is only for Payments whose authorization outcome is `pending`.

```sh
curl -i http://localhost:8080/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/authorization-retries \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: retry-auth-order-1001' \
  -d '{
    "card": {
      "number": "4111111111111111",
      "cvv": "123",
      "expiry_month": 12,
      "expiry_year": 2030
    }
  }'
```

### Capture, Void, and Refund

Capture, Void, and Refund take no request body. Each operation is full-amount only and must be explicitly requested by the client. Capture and Void are rejected after `authorization_expires_at`.

```sh
curl -i -X POST http://localhost:8080/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/capture \
  -H 'Idempotency-Key: capture-order-1001'
```

```sh
curl -i -X POST http://localhost:8080/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/void \
  -H 'Idempotency-Key: void-order-1001'
```

```sh
curl -i -X POST http://localhost:8080/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/refund \
  -H 'Idempotency-Key: refund-order-1001'
```

Captured response:

```json
{
  "payment": {
    "id": "pay_550e8400-e29b-41d4-a716-446655440000",
    "order_id": "order-1001",
    "customer_id": "customer-501",
    "amount": 1299,
    "currency": "USD",
    "status": "captured",
    "authorization_expires_at": "2026-06-18T13:00:00Z",
    "created_at": "2026-06-18T12:00:00Z",
    "updated_at": "2026-06-18T12:30:00Z"
  }
}
```

### Fetch and Search Payments

```sh
curl -i http://localhost:8080/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000
```

```sh
curl -i 'http://localhost:8080/v1/payments?order_id=order-1001&customer_id=customer-501&status=authorized'
```

Search responses use a `payments` envelope:

```json
{
  "payments": [
    {
      "id": "pay_550e8400-e29b-41d4-a716-446655440000",
      "order_id": "order-1001",
      "customer_id": "customer-501",
      "amount": 1299,
      "currency": "USD",
      "status": "authorized",
      "authorization_expires_at": "2026-06-18T13:00:00Z",
      "created_at": "2026-06-18T12:00:00Z",
      "updated_at": "2026-06-18T12:00:00Z"
    }
  ]
}
```

### Error Envelope

Errors use an `error` envelope with stable machine-readable codes:

```json
{
  "error": {
    "code": "payment_not_found",
    "message": "payment was not found"
  }
}
```

## Test

```sh
go test ./...
```

The default test suite includes Docker-backed integration tests. To run only fast tests:

```sh
go test -short ./...
```

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
MOCK_BANK_BASE_URL                required Mock Bank base URL
FINGERPRINT_SECRET                 required HMAC secret for request and authorization card fingerprints
ADDR                              optional HTTP listen address, defaults to :8080
```

Example:

```sh
export DATABASE_URL='postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable'
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

Start Postgres, apply migrations, and run the API for local development:

```sh
docker compose up
```

The Compose environment starts the API on `http://localhost:8080` with:

```text
ADDR=:8080
DATABASE_URL=postgres://payment_gateway:payment_gateway@postgres:5432/payment_gateway?sslmode=disable
MOCK_BANK_BASE_URL=http://mock-bank:9090
FINGERPRINT_SECRET=local-development-secret
```

The Mock Bank is an external dependency. When using Compose, run or attach a Mock Bank service named `mock-bank` on the Compose network, or override `MOCK_BANK_BASE_URL` to a reachable Mock Bank URL.

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

The first custom metric set is HTTP RED:

```text
http_requests_total{method,route,status}
http_request_duration_seconds{method,route,status}
```

Route labels use bounded route patterns, such as `/v1/payments/{id}`, and never include Payment IDs, Order IDs, Customer IDs, Idempotency Keys, or raw request URIs. The registry also includes Go runtime and process metrics.

## Public API

Mutating endpoints require an `Idempotency-Key` header. Reusing the same key for the same operation and same request fingerprint replays the original response snapshot. Reusing it with different request values returns `409 Conflict`.

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

# Payment Gateway

Payment Gateway is a Go service that will sit between an e-commerce Order Service and a Mock Bank. The current runtime shell starts the service with Payment Gateway configuration, exposes process health and database readiness endpoints, and keeps the existing hexagonal package layout while the payment behavior is implemented in vertical slices.

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

## Requirements

- Go 1.26.1 or newer
- Postgres
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

The service validates that the Mock Bank base URL is configured, but Mock Bank unavailability does not prevent startup.

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

Start the API and Postgres together for local development:

```sh
docker compose up
```

The Compose environment runs Postgres, applies pending SQL migrations, and starts the API on `http://localhost:8080`.

## Operational Endpoints

Process health does not check Postgres or the Mock Bank:

```sh
curl -i http://localhost:8080/healthz
```

Readiness checks Postgres and does not require Mock Bank availability:

```sh
curl -i http://localhost:8080/readyz
```

## Test

```sh
go test ./...
```

The default test suite includes Docker-backed integration tests. To run only fast tests:

```sh
go test -short ./...
```

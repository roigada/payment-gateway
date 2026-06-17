# Payment Gateway

Payment Gateway is a Go service that will sit between an e-commerce Order Service and a Mock Bank. The current runtime shell starts the gateway process, validates required configuration, connects to Postgres, and exposes operational health endpoints.

## Structure

```text
cmd/paymentgateway application wiring and HTTP server startup
internal/domain     domain model and invariants
internal/app        application use cases and ports
internal/httpapi    JSON HTTP adapter
internal/postgres   Postgres adapter
internal/uuidgen    UUID-backed ID generator adapter
migrations          plain SQL database migrations
```

The dependency direction is inward: adapters depend on the application and domain, while the domain does not depend on HTTP, Postgres, UUID libraries, or JSON.

## Requirements

- Go 1.26 or newer
- Postgres
- A tool for applying plain SQL migrations, such as `migrate`
- Docker, if using the local Compose environment

## Configuration

The service reads configuration from environment variables:

```text
DATABASE_URL                     required Postgres connection string
MOCK_BANK_BASE_URL               required Mock Bank base URL
AUTHORIZATION_FINGERPRINT_SECRET required secret for future Authorization Fingerprints
ADDR                             optional HTTP listen address, defaults to :8080
```

Example:

```sh
export DATABASE_URL='postgres://paymentgateway:paymentgateway@localhost:5432/paymentgateway?sslmode=disable'
export MOCK_BANK_BASE_URL='http://localhost:8081'
export AUTHORIZATION_FINGERPRINT_SECRET='dev-secret'
export ADDR=':8080'
```

The gateway does not require the Mock Bank to be reachable at startup. Bank availability is handled by Payment operations once those endpoints are implemented.

## Database

Apply pending migrations before starting the API:

```sh
migrate -path migrations -database "$DATABASE_URL" up
```

Migration files use the `000001_name.up.sql` and `000001_name.down.sql` naming convention. The application does not run migrations itself.

## Run

```sh
go run ./cmd/paymentgateway
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

Readiness checks Postgres only:

```sh
curl -i http://localhost:8080/readyz
```

## Test

```sh
go test ./...
```

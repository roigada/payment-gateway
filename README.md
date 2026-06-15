# template-go

A small Go template for CRUD APIs with hexagonal boundaries and a minimal DDD-flavored domain model.

The example domain is **Task Management**. A **Task** has a stable **Task ID**, a required non-empty **Title**, and one completion state. Tasks can be created, listed, retrieved, marked complete, reopened, and hard-deleted.

## Creating a New Project

Use this template as a one-time seed for a new service, not as a long-lived fork that stays synced with `template-go`.

First make this repository available as a GitHub template repository:

```sh
gh repo edit roigada/template-go --template
```

Then create the new repository from the GitHub template UI, or with the GitHub CLI:

```sh
gh repo create OWNER/NEW_REPO --template roigada/template-go --private --clone
```

Keep the adaptation manual until repeated new projects prove that a script would remove real duplication without hiding stale sample behavior.

Recommended first adaptation pass:

1. Create the new repository from this template.
2. Commit the mechanical identity changes separately: rename the module in `go.mod`, rename `cmd/taskapi` to the new service command name, update import paths, update service names, and update README examples.
3. Rewrite `CONTEXT.md` before replacing the sample code so the new project's glossary drives names for domain types, use cases, routes, migrations, and tests.
4. Commit the domain replacement separately: replace the sample Task domain with the new project's first real domain slice, including use cases, routes, migrations, and tests. Keep the generic layer package layout for a single-context service; rename the domain types and files inside those packages.
5. Preserve the template's HTTP API conventions by default: version public resource routes under `/v1`, keep operational routes such as `/healthz` outside `/v1`, use explicit action endpoints for domain commands, return enveloped JSON resources and stable error codes, distinguish invalid JSON from domain-rule violations, and return relative `Location` headers for created resources.
6. Keep architecture and tooling ADRs that still apply. Rewrite or delete Task-specific ADRs so no ADR describes sample behavior that is absent from the new project.
7. Delete `internal/exampleapi` and related notification configuration unless the new project has a real outbound integration on day one.
8. Keep Postgres, plain SQL migrations, and the repository adapter for CRUD APIs that own durable records; remove them only for stateless services, workers, CLIs, or services without durable local state.
9. Do not add authentication during the template adaptation unless the new project's first real use case requires it.
10. Preserve the lightweight test strategy at first: domain, application, HTTP, and focused adapter tests with fakes. Add Postgres integration tests only when the new SQL behavior is complex enough to need them.
11. Run `go test ./...` and `docker compose up` after the adaptation.

## Structure

```text
cmd/taskapi        application wiring and HTTP server startup
internal/domain   Task model and invariants
internal/app      Task use cases and ports
internal/exampleapi Example third-party HTTP notification adapter
internal/httpapi  JSON HTTP adapter
internal/postgres Postgres repository adapter
internal/uuidgen  UUID-backed Task ID generator adapter
migrations        plain SQL database migrations
```

The dependency direction is inward: adapters depend on the application and domain, while the domain does not depend on HTTP, Postgres, UUID libraries, or JSON.

## Requirements

- Go 1.25 or newer
- Postgres
- A tool for applying plain SQL migrations, such as `migrate`
- Docker, if using the local Compose environment

## Configuration

The service reads configuration from environment variables:

```text
DATABASE_URL  required Postgres connection string
ADDR          optional HTTP listen address, defaults to :8080
EXAMPLE_API_BASE_URL optional Example API base URL for Task notifications
EXAMPLE_API_TOKEN    required when EXAMPLE_API_BASE_URL is set
```

Example:

```sh
export DATABASE_URL='postgres://taskapi:taskapi@localhost:5432/taskapi?sslmode=disable'
export ADDR=':8080'
```

When `EXAMPLE_API_BASE_URL` is set, the service sends Bearer-authenticated Task creation and completion notifications to the Example API. When it is absent, the service uses a no-op notifier so local development only needs Postgres.

## Database

Apply pending migrations before starting the API:

```sh
migrate -path migrations -database "$DATABASE_URL" up
```

Migration files use the `000001_name.up.sql` and `000001_name.down.sql` naming convention. The application does not run migrations itself.

## Run

```sh
go run ./cmd/taskapi
```

## Run With Docker Compose

Start the API and Postgres together for local development:

```sh
docker compose up
```

The Compose environment runs Postgres, applies pending SQL migrations, and starts the API on `http://localhost:8080`.

## Test

```sh
go test ./...
```

The baseline tests focus on domain behavior and application/HTTP behavior with in-memory fakes. Postgres integration tests are intentionally not part of the initial template.

## API

Healthcheck:

```sh
curl -i http://localhost:8080/healthz
```

Create a Task:

```sh
curl -i \
  -X POST http://localhost:8080/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Buy milk"}'
```

List Tasks:

```sh
curl -i http://localhost:8080/v1/tasks
```

Get a Task:

```sh
curl -i http://localhost:8080/v1/tasks/{task_id}
```

Complete a Task:

```sh
curl -i -X POST http://localhost:8080/v1/tasks/{task_id}/complete
```

Reopen a Task:

```sh
curl -i -X POST http://localhost:8080/v1/tasks/{task_id}/reopen
```

Delete a Task:

```sh
curl -i -X DELETE http://localhost:8080/v1/tasks/{task_id}
```

## Deliberate Omissions

This template intentionally leaves out ORMs, sqlc, in-app migration execution, production app containerization, soft delete, timestamps, authentication, OpenAPI generation, CQRS, event buses, and generic partial-update endpoints. Add those only when a real application needs them.

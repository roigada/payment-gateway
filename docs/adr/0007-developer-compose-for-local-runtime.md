# Developer Compose for local runtime

The repository provides a developer-mode Docker Compose setup that runs the Go API and Postgres together for local development. The Compose setup improves local startup while keeping production packaging out of scope: the API runs from the source tree with `go run`, Postgres data is kept in a named Docker volume, pending SQL migrations run through a one-shot migration service with migration history, and startup ordering uses Postgres health checks so the API waits for a ready, migrated database.

The Mock Bank remains an external dependency. Local development can either attach a `mock-bank` service to the Compose network or override `MOCK_BANK_BASE_URL` to a reachable Mock Bank URL.

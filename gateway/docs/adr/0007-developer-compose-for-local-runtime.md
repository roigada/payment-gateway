# Developer Compose for local runtime

The repository provides a developer-mode Docker Compose setup that runs the Go API and Postgres together for local development. The Compose setup improves local startup: the API runs from the source tree with `go run`, Postgres data is kept in a named Docker volume, the current SQL schema runs through a one-shot migration service, and startup ordering uses Postgres health checks so the API waits for a ready, migrated database. Production packaging is intentionally separate in `gateway/Dockerfile`; it builds only the authored gateway runtime and leaves migration execution to deployment infrastructure.

The Mock Bank remains an external dependency. Local development can either attach a `mock-bank` service to the Compose network or override `MOCK_BANK_BASE_URL` to a reachable Mock Bank URL.

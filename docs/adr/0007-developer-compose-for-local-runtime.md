# Developer Compose for local runtime

This template provides a developer-mode Docker Compose setup that runs the Go API and Postgres together for local development. The Compose setup improves one-command local startup while keeping production packaging out of scope: the API runs from the source tree with `go run`, Postgres data is kept in a named Docker volume, pending SQL migrations run through a one-shot migration service with migration history, and startup ordering uses Postgres health checks so the API waits for a ready, migrated database.

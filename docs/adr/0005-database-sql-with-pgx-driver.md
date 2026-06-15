# database/sql with pgx driver

The Postgres adapter uses Go's `database/sql` package with the pgx stdlib driver. This keeps SQL explicit at the adapter boundary and avoids introducing an ORM or code generation into the starter template before the query surface is large enough to justify it.

# database/sql with pgx driver

The Postgres adapter uses Go's `database/sql` package with the pgx stdlib driver. This keeps SQL explicit at the adapter boundary and avoids introducing an ORM or code generation while the payment query surface is still small enough to keep hand-written SQL readable.

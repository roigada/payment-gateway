# Structured logging with slog

The API uses the standard library `log/slog` package as its project-facing logging API and emits structured JSON logs from the process entrypoint. Logging stays at runtime and adapter boundaries: `cmd/payment-gateway` logs process lifecycle failures, the HTTP adapter logs one completion event per request, and panic recovery logs recovered panics with stack traces. Payment-specific operational signal is recorded through metrics instead of request log enrichment, which keeps the domain code independent of logging and observability libraries.

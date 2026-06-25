# Structured logging with slog

The API uses the standard library `log/slog` package as its project-facing logging API and emits structured JSON logs from the process entrypoint. Logging stays at runtime and adapter boundaries: `cmd/payment-gateway` logs process lifecycle failures, the HTTP adapter logs one completion event per request with safe operational context, and panic recovery logs recovered panics with stack traces. This keeps the gateway dependency-light while leaving room to add tracing, metrics, or a different `slog.Handler` later without changing domain code.

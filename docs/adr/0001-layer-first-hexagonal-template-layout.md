# Layer-first hexagonal layout

The payment gateway uses a small layer-first Go layout: `cmd/payment-gateway` wires the application, `internal/domain` owns Payment invariants and lifecycle transitions, `internal/app` owns use cases and ports, and `internal/postgres`, `internal/httpapi`, `internal/mockbank`, and `internal/uuidgen` implement adapters. This keeps the codebase easy to navigate while preserving dependency direction between domain, application, and adapters.

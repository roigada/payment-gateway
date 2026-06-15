# Layer-first hexagonal template layout

This template uses a small layer-first Go layout: `cmd/taskapi` wires the application, `internal/domain` owns task invariants, `internal/app` owns use cases and ports, and `internal/postgres` and `internal/httpapi` implement adapters. This keeps the starter structure easy to read and copy while preserving dependency direction between domain, application, and adapters.

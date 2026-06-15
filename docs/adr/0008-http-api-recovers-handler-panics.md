# HTTP API recovers handler panics

The HTTP adapter owns request-boundary concerns, so it wraps its routes with panic recovery instead of relying on `net/http`'s default connection-level recovery. Recovered panics are logged through the server logger and returned as the existing generic JSON internal server error response when the response has not started, keeping the public API contract stable. If a panic happens after the response has started, recovery only logs the panic and marks the connection to close because the status, headers, or body may already be committed and cannot be replaced with a clean error response.

`Server` stores only the final composed `http.Handler`; its `routes` method builds a mux, registers routes, applies route-specific middleware where needed, and returns the server-wide panic recovery wrapper. This keeps the mux as construction detail while leaving one obvious place to compose future middleware.

`recoverPanic` preserves `http.ErrAbortHandler` by re-panicking it, matching `net/http`'s intentional abort behavior instead of turning that sentinel into an application JSON error.

# Use Prometheus Metrics for Gateway Observability

The gateway exposes Prometheus-format metrics from the application through `/metrics` so local and deployed environments can scrape the same operational signal. Go code records bounded, low-cardinality service metrics, starting with HTTP RED metrics plus Go runtime and process metrics; Grafana consumes Prometheus externally and is not an application dependency. Payment operation metrics and Mock Bank dependency metrics can be added later once the HTTP metric contract is stable.

# Assess Observability Beyond Prometheus Metrics

Research ticket: [Assess observability beyond Prometheus metrics](https://github.com/roigada/payment-gateway/issues/76)

## Question

Given the existing structured logs, Prometheus metrics, and Grafana dashboard, would tracing, log correlation, alerting, SLOs, or runbooks add the strongest next observability portfolio signal?

## Local Findings

- The gateway already exposes Prometheus metrics for HTTP RED, Payment operation outcomes, Mock Bank dependency RED, Postgres pool USE, Go runtime, and process metrics. Source: [gateway/README.md](../../README.md), [ADR-0020](../adr/0020-use-prometheus-metrics-for-gateway-observability.md).
- The root demo already starts Prometheus and Grafana, with a provisioned Gateway Overview dashboard. The README gives reviewers a concrete path: run `make demo`, run `make demo-smoke`, then inspect Grafana and Prometheus. Source: [README.md](../../../README.md), [compose.yaml](../../../compose.yaml), [observability/grafana/dashboards/gateway-overview.json](../../../observability/grafana/dashboards/gateway-overview.json).
- Grafana alert provisioning is present but empty: [observability/grafana/provisioning/alerting/empty.yml](../../../observability/grafana/provisioning/alerting/empty.yml) contains only `apiVersion: 1`.
- Prometheus has a short local scrape and evaluation interval but no rule files configured. Source: [observability/prometheus/prometheus.yml](../../../observability/prometheus/prometheus.yml).
- Structured JSON request logs already exist at the HTTP/runtime boundary, while ADR-0009 keeps payment-specific operational signal in metrics instead of domain log enrichment. Source: [ADR-0009](../adr/0009-structured-logging-with-slog.md), [gateway/internal/httpapi/middleware.go](../../internal/httpapi/middleware.go).
- ADR-0020 intentionally left Postgres server diagnostics, synthetic load generation, alert rules, and Alertmanager out of the first observability slice, so alerting/runbooks are a clean follow-up rather than a reversal.

## External Findings

- Prometheus alerting rules define alert conditions with PromQL expressions, optional `for` and `keep_firing_for` timing, labels, and annotations; annotations are a natural place for descriptions and runbook links. Source: [Prometheus alerting rules](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/).
- Google's SRE workbook frames SLO alerting around precision, recall, detection time, and reset time, and recommends burn-rate alerting for defending an error budget. Source: [Google SRE Workbook: Prometheus Alerting: Turn SLOs into Alerts](https://sre.google/workbook/alerting-on-slos/).
- Grafana OSS can provision alerting resources from YAML files, so alert rules, contact points, and notification policies can be version-controlled and loaded by the existing Compose demo. Source: [Grafana alerting file provisioning](https://grafana.com/docs/grafana/latest/alerting/set-up/provision-alerting-resources/file-provisioning/).
- OpenTelemetry tracing is valuable for understanding request paths and service interactions, especially in distributed systems, but it would add a new telemetry pipeline and backend for a gateway that currently has one authored service plus a bundled Mock Bank. Source: [OpenTelemetry traces](https://opentelemetry.io/docs/concepts/signals/traces/).

## Options Considered

1. **SLO-flavored alert rules plus runbook links**
   - Builds directly on metrics and the existing Grafana demo.
   - Strong reviewer signal because it shows the difference between business outcomes such as Declined Payments and operational symptoms such as Mock Bank timeouts, elevated 5xx responses, and Postgres pool saturation.
   - Version-controlled alert definitions and runbook links are easy to inspect without a live incident.

2. **Runbooks without alert rules**
   - Useful for reviewer clarity, but weaker alone because there is no explicit condition that tells an operator when to use each runbook.
   - Best paired with alert annotations and dashboard panels.

3. **Request/log correlation**
   - Helpful for debugging, especially if a generated request identifier appears in every HTTP response and request log entry.
   - Lower immediate signal than alerting because there is no log aggregation backend in the demo. It can be a small supporting item if runbooks tell reviewers how to inspect local container logs.

4. **Distributed tracing**
   - Technically current and recognizable, especially with OpenTelemetry.
   - Lower fit for the next roadmap item because this repository currently has a single authored gateway and an external Mock Bank dependency already covered by dependency metrics. Adding an OpenTelemetry Collector plus Tempo/Jaeger would expand infrastructure more than it clarifies the strongest backend story.

5. **Log aggregation**
   - Could pair with request/log correlation, but it adds another backend before the project has alert rules or runbooks.
   - Weak immediate portfolio value compared with making existing metrics operational.

6. **Alertmanager-style notification routing**
   - Valuable in production, but less important for a local portfolio demo than provisioned rules and runbook links.
   - Can be deferred unless a hosted deployment effort later needs real notifications.

## Decision

The roadmap should prioritize **SLO-flavored alert rules with runbook links** as the strongest next observability layer.

Recommended later implementation shape:

1. Define a small set of gateway-owned operational symptoms from existing metrics:
   - elevated 5xx rate on gateway HTTP requests.
   - elevated Mock Bank dependency failures, especially `timeout`, `unavailable`, and `internal`.
   - elevated Payment operation technical failures such as `bank_timeout`, `bank_unavailable`, `internal`, and possibly `idempotency_in_progress` if stale recovery is not yet implemented.
   - Postgres pool saturation or sustained wait growth.
2. Add alert definitions through Grafana provisioning, or Prometheus rule files if the implementation chooses Prometheus-first alerts. Keep them version-controlled.
3. Include alert annotations that link to short runbooks under `gateway/docs/runbooks/`.
4. Keep the runbooks local-demo friendly: how to reproduce signal with `make demo` and `make demo-smoke`, what Grafana/Prometheus panels to inspect, and what likely causes mean in gateway language.
5. Avoid paging/contact-point complexity in the first slice. A visible Grafana alerting page and version-controlled rules are enough for the portfolio roadmap.
6. Defer distributed tracing and log aggregation until after CI, API contract, core reliability, and alert/runbook work are in place. If tracing is revisited, scope it as OpenTelemetry trace propagation across HTTP and Mock Bank calls with a local trace backend, not as a replacement for metrics.
7. Treat request/log correlation as optional polish: useful if it is cheap and documented, but it should not outrank alert rules and runbooks.

## Implications for Later Specs

- The observability implementation ticket should be framed as "production-shaped alerting and runbooks for existing metrics," not as generic tracing.
- The work should preserve ADR-0020's boundary: observe the Mock Bank through gateway outbound dependency metrics, not by scraping Mock Bank internals.
- Alert wording should use gateway terms such as Payment, Payment Status, Mock Bank, and Postgres pool, and should avoid classifying normal business outcomes like Declined Payments as outages.
- If the final roadmap ranks observability below CI, OpenAPI, and idempotency recovery, this ticket is still a clear next layer once those higher-signal items are done.

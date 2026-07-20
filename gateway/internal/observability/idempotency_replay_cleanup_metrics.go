package observability

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// IdempotencyReplayCleanupMetrics makes completed Idempotency Replay retention
// observable without accepting caller-controlled labels.
type IdempotencyReplayCleanupMetrics struct {
	runsTotal    *prometheus.CounterVec
	deletedTotal prometheus.Counter
}

func NewIdempotencyReplayCleanupMetrics(registry *prometheus.Registry) (*IdempotencyReplayCleanupMetrics, error) {
	if registry == nil {
		return nil, fmt.Errorf("prometheus registry is required")
	}

	metrics := &IdempotencyReplayCleanupMetrics{
		runsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_gateway_idempotency_replay_cleanup_runs_total",
			Help: "Total completed idempotency replay cleanup runs by bounded result.",
		}, []string{"result"}),
		deletedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "payment_gateway_idempotency_replay_cleanup_records_deleted_total",
			Help: "Total completed idempotency replay records deleted by cleanup.",
		}),
	}
	if err := registry.Register(metrics.runsTotal); err != nil {
		return nil, err
	}
	if err := registry.Register(metrics.deletedTotal); err != nil {
		return nil, err
	}
	return metrics, nil
}

func (m *IdempotencyReplayCleanupMetrics) RecordIdempotencyReplayCleanup(result string, removed int) {
	if !isIdempotencyReplayCleanupResult(result) || removed < 0 {
		return
	}
	m.runsTotal.WithLabelValues(result).Inc()
	if result != "failed" {
		m.deletedTotal.Add(float64(removed))
	}
}

func isIdempotencyReplayCleanupResult(result string) bool {
	switch result {
	case "completed", "empty", "failed":
		return true
	default:
		return false
	}
}

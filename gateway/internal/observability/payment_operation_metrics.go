package observability

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type PaymentOperationMetrics struct {
	operationsTotal   *prometheus.CounterVec
	operationDuration *prometheus.HistogramVec
	recoveryTotal     *prometheus.CounterVec
}

func NewPaymentOperationMetrics(registry *prometheus.Registry) (*PaymentOperationMetrics, error) {
	if registry == nil {
		return nil, fmt.Errorf("prometheus registry is required")
	}

	metrics := &PaymentOperationMetrics{
		operationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_gateway_payment_operations_total",
			Help: "Total number of payment operations handled by the payment gateway.",
		}, []string{"operation", "outcome"}),
		operationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "payment_gateway_payment_operation_duration_seconds",
			Help:    "Duration of payment operations handled by the payment gateway.",
			Buckets: httpRequestDurationBuckets,
		}, []string{"operation", "outcome"}),
		recoveryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_gateway_idempotency_recovery_total",
			Help: "Total number of stuck idempotency claim recovery events.",
		}, []string{"operation", "result"}),
	}

	if err := registry.Register(metrics.operationsTotal); err != nil {
		return nil, err
	}
	if err := registry.Register(metrics.operationDuration); err != nil {
		return nil, err
	}
	if err := registry.Register(metrics.recoveryTotal); err != nil {
		return nil, err
	}

	return metrics, nil
}

func (m *PaymentOperationMetrics) RecordPaymentOperation(operation string, outcome string, duration time.Duration) {
	m.operationsTotal.WithLabelValues(operation, outcome).Inc()
	m.operationDuration.WithLabelValues(operation, outcome).Observe(duration.Seconds())
}

func (m *PaymentOperationMetrics) RecordIdempotencyRecovery(operation string, result string) {
	m.recoveryTotal.WithLabelValues(operation, result).Inc()
}

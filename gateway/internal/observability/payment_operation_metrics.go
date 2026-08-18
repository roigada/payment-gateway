package observability

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/roigada/payment-gateway/internal/app"
)

type PaymentOperationMetrics struct {
	operationsTotal   *prometheus.CounterVec
	operationDuration *prometheus.HistogramVec
	recoveryTotal     *prometheus.CounterVec
	releaseFailures   *prometheus.CounterVec
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
		releaseFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_gateway_payment_command_release_failures_total",
			Help: "Total number of failed payment command idempotency claim releases.",
		}, []string{"operation"}),
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
	if err := registry.Register(metrics.releaseFailures); err != nil {
		return nil, err
	}

	return metrics, nil
}

func (m *PaymentOperationMetrics) RecordPaymentOperation(operation string, outcome string, duration time.Duration) {
	m.operationsTotal.WithLabelValues(operation, outcome).Inc()
	m.operationDuration.WithLabelValues(operation, outcome).Observe(duration.Seconds())
}

func (m *PaymentOperationMetrics) RecordIdempotencyRecovery(operation string, result string) {
	if !isPaymentOperation(operation) || !isIdempotencyRecoveryResult(result) {
		return
	}
	m.recoveryTotal.WithLabelValues(operation, result).Inc()
}

func (m *PaymentOperationMetrics) RecordPaymentCommandReleaseFailure(operation string) {
	if !isPaymentOperation(operation) {
		return
	}
	m.releaseFailures.WithLabelValues(operation).Inc()
}

func isPaymentOperation(operation string) bool {
	switch operation {
	case app.AuthorizePaymentOperation, app.RetryAuthorizationOperation, app.CapturePaymentOperation, app.VoidPaymentOperation, app.RefundPaymentOperation:
		return true
	default:
		return false
	}
}

func isIdempotencyRecoveryResult(result string) bool {
	switch result {
	case app.IdempotencyRecoveryAttempted, app.IdempotencyRecoveryRecovered, app.IdempotencyRecoveryUnrecoverable, app.IdempotencyRecoveryConflict:
		return true
	default:
		return false
	}
}

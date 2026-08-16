package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that payment operation metrics records operation count and duration.
func TestPaymentOperationMetricsRecordsOperationCountAndDuration(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewPaymentOperationMetrics(registry)
	require.NoError(t, err)

	metrics.RecordPaymentOperation("capture_payment", "captured", 250*time.Millisecond)

	families, err := registry.Gather()
	require.NoError(t, err)

	operations := metricFamilyByName(t, families, "payment_gateway_payment_operations_total")
	require.Len(t, operations.GetMetric(), 1)
	assert.Equal(t, float64(1), operations.GetMetric()[0].GetCounter().GetValue())
	assertMetricLabels(t, operations.GetMetric()[0].GetLabel(), map[string]string{
		"operation": "capture_payment",
		"outcome":   "captured",
	})

	duration := metricFamilyByName(t, families, "payment_gateway_payment_operation_duration_seconds")
	require.Len(t, duration.GetMetric(), 1)
	assert.Equal(t, uint64(1), duration.GetMetric()[0].GetHistogram().GetSampleCount())
	assert.Equal(t, 0.25, duration.GetMetric()[0].GetHistogram().GetSampleSum())
	assertMetricLabels(t, duration.GetMetric()[0].GetLabel(), map[string]string{
		"operation": "capture_payment",
		"outcome":   "captured",
	})
}

// Verifies that payment operation metrics records idempotency recovery.
func TestPaymentOperationMetricsRecordsIdempotencyRecovery(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewPaymentOperationMetrics(registry)
	require.NoError(t, err)

	metrics.RecordIdempotencyRecovery("capture_payment", "attempted")

	families, err := registry.Gather()
	require.NoError(t, err)

	recovery := metricFamilyByName(t, families, "payment_gateway_idempotency_recovery_total")
	require.Len(t, recovery.GetMetric(), 1)
	assert.Equal(t, float64(1), recovery.GetMetric()[0].GetCounter().GetValue())
	assertMetricLabels(t, recovery.GetMetric()[0].GetLabel(), map[string]string{
		"operation": "capture_payment",
		"result":    "attempted",
	})
}

// Verifies that payment operation metrics records payment command release failure.
func TestPaymentOperationMetricsRecordsPaymentCommandReleaseFailure(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewPaymentOperationMetrics(registry)
	require.NoError(t, err)

	metrics.RecordPaymentCommandReleaseFailure("capture_payment")

	families, err := registry.Gather()
	require.NoError(t, err)

	releaseFailures := metricFamilyByName(t, families, "payment_gateway_payment_command_release_failures_total")
	require.Len(t, releaseFailures.GetMetric(), 1)
	assert.Equal(t, float64(1), releaseFailures.GetMetric()[0].GetCounter().GetValue())
	assertMetricLabels(t, releaseFailures.GetMetric()[0].GetLabel(), map[string]string{
		"operation": "capture_payment",
	})
}

// Verifies that payment operation metrics only records bounded idempotency recovery labels.
func TestPaymentOperationMetricsOnlyRecordsBoundedIdempotencyRecoveryLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewPaymentOperationMetrics(registry)
	require.NoError(t, err)

	metrics.RecordIdempotencyRecovery("capture_payment", "recovered")
	metrics.RecordIdempotencyRecovery("public-idempotency-key-123", "recovered")
	metrics.RecordIdempotencyRecovery("capture_payment", "bank authorization bank_auth_123")

	families, err := registry.Gather()
	require.NoError(t, err)

	recovery := metricFamilyByName(t, families, "payment_gateway_idempotency_recovery_total")
	require.Len(t, recovery.GetMetric(), 1)
	assert.Equal(t, float64(1), recovery.GetMetric()[0].GetCounter().GetValue())
	assertMetricLabels(t, recovery.GetMetric()[0].GetLabel(), map[string]string{
		"operation": "capture_payment",
		"result":    "recovered",
	})
}

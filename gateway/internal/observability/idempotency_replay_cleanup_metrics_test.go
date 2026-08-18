package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that idempotency replay cleanup metrics record bounded outcomes and deleted records.
func TestIdempotencyReplayCleanupMetricsRecordBoundedOutcomesAndDeletedRecords(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewIdempotencyReplayCleanupMetrics(registry)
	require.NoError(t, err)

	metrics.RecordIdempotencyReplayCleanup("completed", 3)
	metrics.RecordIdempotencyReplayCleanup("empty", 0)
	metrics.RecordIdempotencyReplayCleanup("failed", 0)
	metrics.RecordIdempotencyReplayCleanup("public-idempotency-key-123", 5)
	metrics.RecordIdempotencyReplayCleanup("completed", -1)

	families, err := registry.Gather()
	require.NoError(t, err)

	runs := metricFamilyByName(t, families, "payment_gateway_idempotency_replay_cleanup_runs_total")
	require.Len(t, runs.GetMetric(), 3)
	for _, metric := range runs.GetMetric() {
		assert.NotEqual(t, "public-idempotency-key-123", metric.GetLabel()[0].GetValue())
	}

	deleted := metricFamilyByName(t, families, "payment_gateway_idempotency_replay_cleanup_records_deleted_total")
	require.Len(t, deleted.GetMetric(), 1)
	assert.Equal(t, float64(3), deleted.GetMetric()[0].GetCounter().GetValue())
}

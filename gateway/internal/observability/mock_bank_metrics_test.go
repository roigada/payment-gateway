package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that mock bank metrics records request count and duration.
func TestMockBankMetricsRecordsRequestCountAndDuration(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMockBankMetrics(registry)
	require.NoError(t, err)

	metrics.RecordMockBankRequest("capture", "state_conflict", 1500*time.Millisecond)

	families, err := registry.Gather()
	require.NoError(t, err)

	requests := metricFamilyByName(t, families, "payment_gateway_mock_bank_requests_total")
	require.Len(t, requests.GetMetric(), 1)
	assert.Equal(t, float64(1), requests.GetMetric()[0].GetCounter().GetValue())
	assertMetricLabels(t, requests.GetMetric()[0].GetLabel(), map[string]string{
		"operation": "capture",
		"result":    "state_conflict",
	})

	duration := metricFamilyByName(t, families, "payment_gateway_mock_bank_request_duration_seconds")
	require.Len(t, duration.GetMetric(), 1)
	assert.Equal(t, uint64(1), duration.GetMetric()[0].GetHistogram().GetSampleCount())
	assert.Equal(t, 1.5, duration.GetMetric()[0].GetHistogram().GetSampleSum())
	assertMetricLabels(t, duration.GetMetric()[0].GetLabel(), map[string]string{
		"operation": "capture",
		"result":    "state_conflict",
	})
}

// Verifies that mock bank metrics records retry outcomes.
func TestMockBankMetricsRecordsRetryOutcomes(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMockBankMetrics(registry)
	require.NoError(t, err)

	metrics.RecordMockBankRetry("authorize", "attempted")
	metrics.RecordMockBankRetry("authorize", "succeeded")

	families, err := registry.Gather()
	require.NoError(t, err)
	retries := metricFamilyByName(t, families, "payment_gateway_mock_bank_retries_total")
	require.Len(t, retries.GetMetric(), 2)
}

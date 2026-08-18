package observability

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that pending payment collector reports aggregate pending visibility.
func TestPendingPaymentCollectorReportsAggregatePendingVisibility(t *testing.T) {
	registry := prometheus.NewRegistry()
	collector, err := NewPendingPaymentCollector(pendingPaymentMetricsSource{
		count:            2,
		oldestAgeSeconds: 301.5,
	})
	require.NoError(t, err)
	require.NoError(t, registry.Register(collector))

	families, err := registry.Gather()
	require.NoError(t, err)

	pending := metricFamilyByName(t, families, "payment_gateway_pending_payments_total")
	require.Len(t, pending.GetMetric(), 1)
	assert.Empty(t, pending.GetMetric()[0].GetLabel())
	assert.Equal(t, float64(2), pending.GetMetric()[0].GetGauge().GetValue())

	oldest := metricFamilyByName(t, families, "payment_gateway_oldest_pending_payment_age_seconds")
	require.Len(t, oldest.GetMetric(), 1)
	assert.Empty(t, oldest.GetMetric()[0].GetLabel())
	assert.Equal(t, 301.5, oldest.GetMetric()[0].GetGauge().GetValue())
}

// Verifies that pending payment collector reports zero oldest age when no pending payments exist.
func TestPendingPaymentCollectorReportsZeroOldestAgeWhenNoPendingPaymentsExist(t *testing.T) {
	registry := prometheus.NewRegistry()
	collector, err := NewPendingPaymentCollector(pendingPaymentMetricsSource{})
	require.NoError(t, err)
	require.NoError(t, registry.Register(collector))

	families, err := registry.Gather()
	require.NoError(t, err)

	assert.Equal(t, float64(0), metricFamilyByName(t, families, "payment_gateway_pending_payments_total").GetMetric()[0].GetGauge().GetValue())
	assert.Equal(t, float64(0), metricFamilyByName(t, families, "payment_gateway_oldest_pending_payment_age_seconds").GetMetric()[0].GetGauge().GetValue())
}

// Verifies that new pending payment collector requires pending payment reader.
func TestNewPendingPaymentCollectorRequiresPendingPaymentReader(t *testing.T) {
	collector, err := NewPendingPaymentCollector(nil)

	require.Error(t, err)
	assert.Nil(t, collector)
	assert.Contains(t, err.Error(), "pending payment reader is required")
}

type pendingPaymentMetricsSource struct {
	count            int64
	oldestAgeSeconds float64
	err              error
}

func (s pendingPaymentMetricsSource) PendingPaymentMetrics(context.Context) (int64, float64, error) {
	return s.count, s.oldestAgeSeconds, s.err
}

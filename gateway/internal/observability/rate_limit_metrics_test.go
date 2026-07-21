package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitMetricsRecordsOnlyBoundedRouteClasses(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewRateLimitMetrics(registry)
	require.NoError(t, err)

	metrics.RecordRateLimitRejection("read")
	metrics.RecordRateLimitRejection("write")
	metrics.RecordRateLimitRejection("service-principal-123")
	metrics.RecordRateLimitRejection("/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000")

	families, err := registry.Gather()
	require.NoError(t, err)

	rejections := metricFamilyByName(t, families, "payment_gateway_rate_limit_rejections_total")
	require.Len(t, rejections.GetMetric(), 2)

	valuesByRouteClass := make(map[string]float64, len(rejections.GetMetric()))
	for _, metric := range rejections.GetMetric() {
		labels := metric.GetLabel()
		require.Len(t, labels, 1)
		assert.Equal(t, "route_class", labels[0].GetName())
		valuesByRouteClass[labels[0].GetValue()] = metric.GetCounter().GetValue()
	}
	assert.Equal(t, map[string]float64{"read": 1, "write": 1}, valuesByRouteClass)
}

func TestNewRateLimitMetricsRequiresRegistry(t *testing.T) {
	metrics, err := NewRateLimitMetrics(nil)

	assert.Nil(t, metrics)
	assert.EqualError(t, err, "prometheus registry is required")
}

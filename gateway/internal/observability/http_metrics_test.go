package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that http metrics records request count and duration.
func TestHTTPMetricsRecordsRequestCountAndDuration(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewHTTPMetrics(registry)
	require.NoError(t, err)

	metrics.RecordHTTPRequest("POST", "/api/v1/payments/{payment_id}/capture", 200, 125*time.Millisecond)

	families, err := registry.Gather()
	require.NoError(t, err)

	requests := metricFamilyByName(t, families, "payment_gateway_http_server_requests_total")
	require.Len(t, requests.GetMetric(), 1)
	assert.Equal(t, float64(1), requests.GetMetric()[0].GetCounter().GetValue())
	assertMetricLabels(t, requests.GetMetric()[0].GetLabel(), map[string]string{
		"method": "POST",
		"route":  "/api/v1/payments/{payment_id}/capture",
		"code":   "200",
	})

	duration := metricFamilyByName(t, families, "payment_gateway_http_server_request_duration_seconds")
	require.Len(t, duration.GetMetric(), 1)
	assert.Equal(t, uint64(1), duration.GetMetric()[0].GetHistogram().GetSampleCount())
	assert.Equal(t, 0.125, duration.GetMetric()[0].GetHistogram().GetSampleSum())
	assertMetricLabels(t, duration.GetMetric()[0].GetLabel(), map[string]string{
		"method": "POST",
		"route":  "/api/v1/payments/{payment_id}/capture",
		"code":   "200",
	})
}

// Verifies that http metrics records only bounded rate limit route classes.
func TestHTTPMetricsRecordsOnlyBoundedRateLimitRouteClasses(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewHTTPMetrics(registry)
	require.NoError(t, err)

	metrics.RecordRateLimitRejection("read")
	metrics.RecordRateLimitRejection("write")
	metrics.RecordRateLimitRejection("service-principal-123")
	metrics.RecordRateLimitRejection("/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000")

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

// Verifies that new http metrics requires registry.
func TestNewHTTPMetricsRequiresRegistry(t *testing.T) {
	metrics, err := NewHTTPMetrics(nil)

	assert.Nil(t, metrics)
	assert.EqualError(t, err, "prometheus registry is required")
}

// Verifies that new registry includes runtime and process collectors.
func TestNewRegistryIncludesRuntimeAndProcessCollectors(t *testing.T) {
	registry := NewRegistry()

	families, err := registry.Gather()
	require.NoError(t, err)

	assert.NotNil(t, metricFamilyByName(t, families, "go_goroutines"))
	assert.NotNil(t, metricFamilyByName(t, families, "process_cpu_seconds_total"))
}

func metricFamilyByName(t *testing.T, families []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()

	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	require.Failf(t, "metric family not found", "missing metric family %q", name)
	return nil
}

func assertMetricLabels(t *testing.T, labels []*dto.LabelPair, expected map[string]string) {
	t.Helper()

	actual := make(map[string]string, len(labels))
	for _, label := range labels {
		actual[label.GetName()] = label.GetValue()
	}
	assert.Equal(t, expected, actual)
}

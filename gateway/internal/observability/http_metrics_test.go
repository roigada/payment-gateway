package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPMetricsRecordsRequestCountAndDuration(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewHTTPMetrics(registry)
	require.NoError(t, err)

	metrics.RecordHTTPRequest("POST", "/v1/payments/{payment_id}/capture", 200, 125*time.Millisecond)

	families, err := registry.Gather()
	require.NoError(t, err)

	requests := metricFamilyByName(t, families, "http_requests_total")
	require.Len(t, requests.GetMetric(), 1)
	assert.Equal(t, float64(1), requests.GetMetric()[0].GetCounter().GetValue())
	assertMetricLabels(t, requests.GetMetric()[0].GetLabel(), map[string]string{
		"method": "POST",
		"route":  "/v1/payments/{payment_id}/capture",
		"status": "200",
	})

	duration := metricFamilyByName(t, families, "http_request_duration_seconds")
	require.Len(t, duration.GetMetric(), 1)
	assert.Equal(t, uint64(1), duration.GetMetric()[0].GetHistogram().GetSampleCount())
	assert.Equal(t, 0.125, duration.GetMetric()[0].GetHistogram().GetSampleSum())
	assertMetricLabels(t, duration.GetMetric()[0].GetLabel(), map[string]string{
		"method": "POST",
		"route":  "/v1/payments/{payment_id}/capture",
		"status": "200",
	})
}

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

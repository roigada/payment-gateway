package observability

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var mockBankRequestDurationBuckets = []float64{
	0.05,
	0.1,
	0.25,
	0.5,
	1,
	1.5,
	2,
	2.5,
	5,
}

type MockBankMetrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

func NewMockBankMetrics(registry *prometheus.Registry) (*MockBankMetrics, error) {
	if registry == nil {
		return nil, fmt.Errorf("prometheus registry is required")
	}

	metrics := &MockBankMetrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mock_bank_requests_total",
			Help: "Total number of Mock Bank requests made by the payment gateway.",
		}, []string{"operation", "result"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "mock_bank_request_duration_seconds",
			Help:    "Duration of Mock Bank requests made by the payment gateway.",
			Buckets: mockBankRequestDurationBuckets,
		}, []string{"operation", "result"}),
	}

	if err := registry.Register(metrics.requestsTotal); err != nil {
		return nil, err
	}
	if err := registry.Register(metrics.requestDuration); err != nil {
		return nil, err
	}

	return metrics, nil
}

func (m *MockBankMetrics) RecordMockBankRequest(operation string, result string, duration time.Duration) {
	m.requestsTotal.WithLabelValues(operation, result).Inc()
	m.requestDuration.WithLabelValues(operation, result).Observe(duration.Seconds())
}

package observability

import (
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var httpRequestDurationBuckets = []float64{
	0.005,
	0.01,
	0.025,
	0.05,
	0.1,
	0.25,
	0.5,
	1,
	2.5,
	5,
}

type HTTPMetrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

func NewHTTPMetrics(registry *prometheus.Registry) (*HTTPMetrics, error) {
	if registry == nil {
		return nil, fmt.Errorf("prometheus registry is required")
	}

	metrics := &HTTPMetrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests handled by the payment gateway.",
		}, []string{"method", "route", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests handled by the payment gateway.",
			Buckets: httpRequestDurationBuckets,
		}, []string{"method", "route", "status"}),
	}

	if err := registry.Register(metrics.requestsTotal); err != nil {
		return nil, err
	}
	if err := registry.Register(metrics.requestDuration); err != nil {
		return nil, err
	}

	return metrics, nil
}

func (m *HTTPMetrics) RecordHTTPRequest(method string, route string, status int, duration time.Duration) {
	statusCode := strconv.Itoa(status)
	m.requestsTotal.WithLabelValues(method, route, statusCode).Inc()
	m.requestDuration.WithLabelValues(method, route, statusCode).Observe(duration.Seconds())
}

package observability

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

type RateLimitMetrics struct {
	rejectionsTotal *prometheus.CounterVec
}

func NewRateLimitMetrics(registry *prometheus.Registry) (*RateLimitMetrics, error) {
	if registry == nil {
		return nil, fmt.Errorf("prometheus registry is required")
	}

	metrics := &RateLimitMetrics{
		rejectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_gateway_rate_limit_rejections_total",
			Help: "Total number of Payment API requests rejected by principal rate limits.",
		}, []string{"route_class"}),
	}
	if err := registry.Register(metrics.rejectionsTotal); err != nil {
		return nil, err
	}
	return metrics, nil
}

func (m *RateLimitMetrics) RecordRateLimitRejection(routeClass string) {
	if routeClass != "read" && routeClass != "write" {
		return
	}
	m.rejectionsTotal.WithLabelValues(routeClass).Inc()
}

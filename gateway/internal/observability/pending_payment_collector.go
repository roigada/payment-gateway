package observability

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// PendingPaymentReader supplies aggregate visibility into current Pending Payments.
// It deliberately exposes no Payment or customer identifiers.
type PendingPaymentReader interface {
	PendingPaymentMetrics(ctx context.Context) (count int64, oldestAgeSeconds float64, err error)
}

// PendingPaymentCollector reports the current number and oldest age of Pending
// Payments at scrape time. It does not modify Payment state or contact the Mock
// Bank.
type PendingPaymentCollector struct {
	reader          PendingPaymentReader
	pendingPayments *prometheus.Desc
	oldestPending   *prometheus.Desc
}

func NewPendingPaymentCollector(reader PendingPaymentReader) (*PendingPaymentCollector, error) {
	if reader == nil {
		return nil, fmt.Errorf("pending payment reader is required")
	}

	return &PendingPaymentCollector{
		reader: reader,
		pendingPayments: prometheus.NewDesc(
			"payment_gateway_pending_payments_total",
			"Current number of Pending Payments in the payment gateway.",
			nil,
			nil,
		),
		oldestPending: prometheus.NewDesc(
			"payment_gateway_oldest_pending_payment_age_seconds",
			"Age in seconds of the oldest current Pending Payment, or zero when none exist.",
			nil,
			nil,
		),
	}, nil
}

func (c *PendingPaymentCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.pendingPayments
	ch <- c.oldestPending
}

func (c *PendingPaymentCollector) Collect(ch chan<- prometheus.Metric) {
	count, oldestAgeSeconds, err := c.reader.PendingPaymentMetrics(context.Background())
	if err != nil {
		return
	}

	ch <- prometheus.MustNewConstMetric(c.pendingPayments, prometheus.GaugeValue, float64(count))
	ch <- prometheus.MustNewConstMetric(c.oldestPending, prometheus.GaugeValue, oldestAgeSeconds)
}

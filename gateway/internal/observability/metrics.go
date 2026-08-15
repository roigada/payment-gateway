package observability

import (
	"database/sql"
	"net/http"
)

// Metrics groups the gateway's registered metrics and their scrape handler.
type Metrics struct {
	Handler                  http.Handler
	HTTP                     *HTTPMetrics
	RateLimit                *RateLimitMetrics
	MockBank                 *MockBankMetrics
	PaymentOperations        *PaymentOperationMetrics
	IdempotencyReplayCleanup *IdempotencyReplayCleanupMetrics
}

// NewMetrics registers every gateway metric and collector.
func NewMetrics(db *sql.DB, pendingPayments PendingPaymentReader) (Metrics, error) {
	registry := NewRegistry()
	httpMetrics, err := NewHTTPMetrics(registry)
	if err != nil {
		return Metrics{}, err
	}
	rateLimitMetrics, err := NewRateLimitMetrics(registry)
	if err != nil {
		return Metrics{}, err
	}
	mockBankMetrics, err := NewMockBankMetrics(registry)
	if err != nil {
		return Metrics{}, err
	}
	paymentOperationMetrics, err := NewPaymentOperationMetrics(registry)
	if err != nil {
		return Metrics{}, err
	}
	cleanupMetrics, err := NewIdempotencyReplayCleanupMetrics(registry)
	if err != nil {
		return Metrics{}, err
	}
	postgresPoolCollector, err := NewPostgresPoolCollector(db)
	if err != nil {
		return Metrics{}, err
	}
	if err := registry.Register(postgresPoolCollector); err != nil {
		return Metrics{}, err
	}
	pendingPaymentCollector, err := NewPendingPaymentCollector(pendingPayments)
	if err != nil {
		return Metrics{}, err
	}
	if err := registry.Register(pendingPaymentCollector); err != nil {
		return Metrics{}, err
	}

	return Metrics{
		Handler:                  NewHandler(registry),
		HTTP:                     httpMetrics,
		RateLimit:                rateLimitMetrics,
		MockBank:                 mockBankMetrics,
		PaymentOperations:        paymentOperationMetrics,
		IdempotencyReplayCleanup: cleanupMetrics,
	}, nil
}

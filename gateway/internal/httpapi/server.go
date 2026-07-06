package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

type Server struct {
	handler   http.Handler
	logger    *slog.Logger
	metrics   httpMetrics
	payments  paymentApplication
	readiness readinessChecker
}

type paymentApplication interface {
	AuthorizePayment(ctx context.Context, command app.AuthorizePaymentCommand) (app.PaymentCommandResult, error)
	RetryAuthorization(ctx context.Context, command app.RetryAuthorizationCommand) (app.PaymentCommandResult, error)
	CapturePayment(ctx context.Context, command app.CapturePaymentCommand) (app.PaymentCommandResult, error)
	VoidPayment(ctx context.Context, command app.VoidPaymentCommand) (app.PaymentCommandResult, error)
	RefundPayment(ctx context.Context, command app.RefundPaymentCommand) (app.PaymentCommandResult, error)
	GetPayment(ctx context.Context, query app.GetPaymentQuery) (app.PaymentResult, error)
	SearchPayments(ctx context.Context, query app.SearchPaymentsQuery) ([]app.PaymentResult, error)
}

type readinessChecker interface {
	CheckReady(ctx context.Context) error
}

type httpMetrics interface {
	RecordHTTPRequest(method string, route string, status int, duration time.Duration)
}

func NewServer(payments paymentApplication, readiness readinessChecker, logger *slog.Logger, metrics httpMetrics, metricsHandler http.Handler) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		logger:    logger,
		metrics:   metrics,
		payments:  payments,
		readiness: readiness,
	}
	server.handler = server.routes(metricsHandler)
	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes(metricsHandler http.Handler) http.Handler {
	mux := http.NewServeMux()

	s.handle(mux, "GET /healthz", s.healthz)
	s.handle(mux, "GET /readyz", s.readyz)
	if metricsHandler != nil {
		mux.Handle("GET /metrics", metricsHandler)
	}
	s.handle(mux, "GET /v1/payments", s.searchPayments)
	s.handle(mux, "GET /v1/payments/{id}", s.getPayment)
	s.handle(mux, "POST /v1/payments", s.authorizePayment)
	s.handle(mux, "POST /v1/payments/{payment_id}/authorization-retries", s.retryAuthorization)
	s.handle(mux, "POST /v1/payments/{payment_id}/capture", s.capturePayment)
	s.handle(mux, "POST /v1/payments/{payment_id}/void", s.voidPayment)
	s.handle(mux, "POST /v1/payments/{payment_id}/refund", s.refundPayment)

	return s.logRequest(mux)
}

func (s *Server) handle(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	_, route, ok := strings.Cut(pattern, " ")
	if !ok {
		route = pattern
	}
	mux.Handle(pattern, s.recordHTTPRequest(route, s.recoverPanic(handler)))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.readiness == nil {
		writeError(w, http.StatusServiceUnavailable, errorCodeServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
		return
	}
	if err := s.readiness.CheckReady(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, errorCodeServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/roigada/payment-gateway/internal/app"
)

type Server struct {
	handler   http.Handler
	logger    *slog.Logger
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

func NewServer(payments paymentApplication, readiness readinessChecker, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		logger:    logger,
		payments:  payments,
		readiness: readiness,
	}
	server.handler = server.routes()
	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /v1/payments", s.searchPayments)
	mux.HandleFunc("GET /v1/payments/{id}", s.getPayment)
	mux.HandleFunc("POST /v1/payments", s.authorizePayment)
	mux.HandleFunc("POST /v1/payments/{payment_id}/authorization-retries", s.retryAuthorization)
	mux.HandleFunc("POST /v1/payments/{payment_id}/capture", s.capturePayment)
	mux.HandleFunc("POST /v1/payments/{payment_id}/void", s.voidPayment)
	mux.HandleFunc("POST /v1/payments/{payment_id}/refund", s.refundPayment)

	return s.logRequest(s.recoverPanic(mux))
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

package httpapi

import (
	"context"
	"errors"
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
	options   ServerOptions
}

type ServerOptions struct {
	PaymentCommandTimeout time.Duration
	PaymentReadTimeout    time.Duration
	ReadinessTimeout      time.Duration
	MaxRequestBodyBytes   int64
}

type requestBodyLimitContextKey struct{}

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

func NewServer(payments paymentApplication, readiness readinessChecker, logger *slog.Logger, metrics httpMetrics, metricsHandler http.Handler, options ...ServerOptions) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	config := ServerOptions{PaymentCommandTimeout: 10 * time.Second, PaymentReadTimeout: 3 * time.Second, ReadinessTimeout: 2 * time.Second, MaxRequestBodyBytes: maxJSONBodyBytes}
	if len(options) > 0 {
		config = options[0]
	}
	server := &Server{
		logger:    logger,
		metrics:   metrics,
		payments:  payments,
		readiness: readiness,
		options:   config,
	}
	server.handler = server.routes(metricsHandler)
	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > s.options.MaxRequestBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, s.options.MaxRequestBodyBytes)
		r = r.WithContext(context.WithValue(r.Context(), requestBodyLimitContextKey{}, s.options.MaxRequestBodyBytes))
	}
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
	ctx, cancel := context.WithTimeout(r.Context(), s.options.ReadinessTimeout)
	defer cancel()
	if err := s.readiness.CheckReady(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, errorCodeServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) withCommandDeadline(w http.ResponseWriter, r *http.Request, call func(context.Context) error) bool {
	ctx, cancel := context.WithTimeout(r.Context(), s.options.PaymentCommandTimeout)
	defer cancel()
	if err := call(ctx); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, errorCodePaymentTimeout, "payment command timed out; retry with the same idempotency key")
			return false
		}
		writePaymentServiceError(w, r, err)
		return false
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodePaymentTimeout, "payment command timed out; retry with the same idempotency key")
		return false
	}
	return true
}

func (s *Server) withReadDeadline(w http.ResponseWriter, r *http.Request, call func(context.Context) error) bool {
	ctx, cancel := context.WithTimeout(r.Context(), s.options.PaymentReadTimeout)
	defer cancel()
	if err := call(ctx); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, errorCodeRequestTimeout, "payment request timed out")
			return false
		}
		writePaymentServiceError(w, r, err)
		return false
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodeRequestTimeout, "payment request timed out")
		return false
	}
	return true
}

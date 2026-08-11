package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/serviceauth"
)

type Handler struct {
	handler   http.Handler
	logger    *slog.Logger
	metrics   httpMetrics
	payments  paymentApplication
	readiness readinessChecker
	options   HandlerOptions
}

type HandlerOptions struct {
	PaymentCommandTimeout time.Duration
	PaymentReadTimeout    time.Duration
	ReadinessTimeout      time.Duration
	MaxRequestBodyBytes   int64
	Authenticator         *serviceauth.Authenticator
	RateLimiter           *RateLimiter
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
	RecordRateLimitRejection(routeClass string)
}

func NewHandler(payments paymentApplication, readiness readinessChecker, logger *slog.Logger, metrics httpMetrics, options HandlerOptions) (*Handler, error) {
	if payments == nil {
		return nil, errors.New("httpapi handler: payment application is required")
	}
	if readiness == nil {
		return nil, errors.New("httpapi handler: readiness checker is required")
	}
	if logger == nil {
		return nil, errors.New("httpapi handler: logger is required")
	}
	if metrics == nil {
		return nil, errors.New("httpapi handler: HTTP metrics recorder is required")
	}
	if options.Authenticator == nil {
		return nil, errors.New("httpapi handler: service authenticator is required")
	}
	if options.RateLimiter == nil {
		return nil, errors.New("httpapi handler: rate limiter is required")
	}

	handler := &Handler{
		logger:    logger,
		metrics:   metrics,
		payments:  payments,
		readiness: readiness,
		options:   options,
	}
	handler.handler = handler.routes()
	return handler, nil
}

func (s *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Handler) routes() http.Handler {
	mux := http.NewServeMux()
	registerRoute := func(pattern string, handler http.HandlerFunc) {
		_, route, ok := strings.Cut(pattern, " ")
		if !ok {
			panic("httpapi: route pattern must include a method")
		}
		mux.Handle(pattern, s.recordHTTPRequest(route, s.recoverPanic(s.limitRequestBody(handler))))
	}

	registerRoute("GET /healthz", s.healthz)
	registerRoute("GET /readyz", s.readyz)
	versioned := http.NewServeMux()
	registerVersionedRoute := func(pattern string, scope serviceauth.Scope, routeClass RouteClass, handler http.HandlerFunc) {
		_, route, _ := strings.Cut(pattern, " ")
		versioned.Handle(pattern, s.recordHTTPRequest(route, s.recoverPanic(s.requireScope(scope, s.limitRate(routeClass, s.limitRequestBody(handler)).ServeHTTP))))
	}
	registerVersionedRoute("GET /api/v1/payments", serviceauth.ScopePaymentsRead, RouteClassRead, s.searchPayments)
	registerVersionedRoute("GET /api/v1/payments/{id}", serviceauth.ScopePaymentsRead, RouteClassRead, s.getPayment)
	registerVersionedRoute("POST /api/v1/payments", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.authorizePayment)
	registerVersionedRoute("POST /api/v1/payments/{payment_id}/authorization-retries", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.retryAuthorization)
	registerVersionedRoute("POST /api/v1/payments/{payment_id}/capture", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.capturePayment)
	registerVersionedRoute("POST /api/v1/payments/{payment_id}/void", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.voidPayment)
	registerVersionedRoute("POST /api/v1/payments/{payment_id}/refund", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.refundPayment)
	mux.Handle("/api/v1/", s.requireAuthentication(versioned))

	return s.logRequest(mux)
}

func (s *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.options.ReadinessTimeout)
	defer cancel()
	if err := s.readiness.CheckReady(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, errorCodeServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Handler) withCommandDeadline(w http.ResponseWriter, r *http.Request, call func(context.Context) error) bool {
	ctx := r.Context()
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

func (s *Handler) commandRequest(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), s.options.PaymentCommandTimeout)
	return r.WithContext(ctx), cancel
}

func (s *Handler) withReadDeadline(w http.ResponseWriter, r *http.Request, call func(context.Context) error) bool {
	ctx, cancel := context.WithTimeout(r.Context(), s.options.PaymentReadTimeout)
	defer cancel()
	if err := call(ctx); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, errorCodeRequestTimeout, "payment read timed out")
			return false
		}
		writePaymentServiceError(w, r, err)
		return false
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodeRequestTimeout, "payment read timed out")
		return false
	}
	return true
}

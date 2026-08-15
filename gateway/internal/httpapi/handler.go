package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/serviceauth"
)

type Handler struct {
	handler       http.Handler
	logger        *slog.Logger
	metrics       httpMetrics
	payments      paymentApplication
	readiness     readinessChecker
	authenticator *serviceauth.Authenticator
	rateLimiter   *RateLimiter
	options       HandlerOptions
}

type HandlerDependencies struct {
	Payments      paymentApplication
	Readiness     readinessChecker
	Logger        *slog.Logger
	Metrics       httpMetrics
	Authenticator *serviceauth.Authenticator
	Clock         Clock
}

type HandlerOptions struct {
	PaymentCommandTimeout time.Duration
	PaymentReadTimeout    time.Duration
	ReadinessTimeout      time.Duration
	MaxRequestBodyBytes   int64
	RateLimit             RateLimitConfig
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

func NewHandler(dependencies HandlerDependencies, options HandlerOptions) (*Handler, error) {
	if dependencies.Payments == nil {
		return nil, errors.New("httpapi handler: payment application is required")
	}
	if dependencies.Readiness == nil {
		return nil, errors.New("httpapi handler: readiness checker is required")
	}
	if dependencies.Logger == nil {
		return nil, errors.New("httpapi handler: logger is required")
	}
	if dependencies.Metrics == nil {
		return nil, errors.New("httpapi handler: HTTP metrics recorder is required")
	}
	if dependencies.Authenticator == nil {
		return nil, errors.New("httpapi handler: service authenticator is required")
	}
	rateLimiter, err := NewRateLimiter(dependencies.Clock, options.RateLimit)
	if err != nil {
		return nil, fmt.Errorf("httpapi handler: %w", err)
	}

	handler := &Handler{
		logger:        dependencies.Logger,
		metrics:       dependencies.Metrics,
		payments:      dependencies.Payments,
		readiness:     dependencies.Readiness,
		authenticator: dependencies.Authenticator,
		rateLimiter:   rateLimiter,
		options:       options,
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
	registerVersionedRoute("GET /api/v1/payments/{payment_id}", serviceauth.ScopePaymentsRead, RouteClassRead, s.getPayment)
	registerVersionedRoute("POST /api/v1/payments", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.authorizePayment)
	registerVersionedRoute("POST /api/v1/payments/{payment_id}/authorization-retries", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.retryAuthorization)
	registerVersionedRoute("POST /api/v1/payments/{payment_id}/capture", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.capturePayment)
	registerVersionedRoute("POST /api/v1/payments/{payment_id}/void", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.voidPayment)
	registerVersionedRoute("POST /api/v1/payments/{payment_id}/refund", serviceauth.ScopePaymentsWrite, RouteClassWrite, s.refundPayment)
	mux.Handle("/api/v1/", s.requireAuthentication(versioned))

	return s.logRequest(s.recoverPanic(mux))
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

func (s *Handler) commandRequest(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), s.options.PaymentCommandTimeout)
	return r.WithContext(ctx), cancel
}

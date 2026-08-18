package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/serviceauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A valid authorization request creates a Payment, forwards its public fields to the application,
// and returns only the API representation so bank details never leak to callers.
func TestPostPaymentsAuthorizesPayment(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.authorizePaymentResult = newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", rec.Header().Get("Location"))
	assert.Equal(t, mustAuthorizePaymentCommand(t), api.payments.authorizePaymentCommand)
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "authorized",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:00:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
}

// Every payment-changing endpoint must reject a read-only credential before it reaches the
// application, while a credential with payments:write is allowed through.
func TestPaymentCommandsRequireWriteScope(t *testing.T) {
	const paymentID = "pay_550e8400-e29b-41d4-a716-446655440000"

	for _, tt := range []struct {
		name       string
		path       string
		body       string
		headers    map[string]string
		wasInvoked func(*paymentApplicationFake) bool
	}{
		{
			name:       "authorize",
			path:       "/api/v1/payments",
			body:       validAuthorizeBody(),
			headers:    map[string]string{"Content-Type": "application/json", "Idempotency-Key": "authorize-key"},
			wasInvoked: func(payments *paymentApplicationFake) bool { return payments.authorizePaymentCalls == 1 },
		},
		{
			name:       "authorization retry",
			path:       "/api/v1/payments/" + paymentID + "/authorization-retries",
			body:       validRetryAuthorizationBody(),
			headers:    map[string]string{"Content-Type": "application/json", "Idempotency-Key": "retry-key"},
			wasInvoked: func(payments *paymentApplicationFake) bool { return payments.retryAuthorizationCalls == 1 },
		},
		{
			name:       "capture",
			path:       "/api/v1/payments/" + paymentID + "/capture",
			headers:    map[string]string{"Idempotency-Key": "capture-key"},
			wasInvoked: func(payments *paymentApplicationFake) bool { return payments.capturePaymentCalls == 1 },
		},
		{
			name:       "void",
			path:       "/api/v1/payments/" + paymentID + "/void",
			headers:    map[string]string{"Idempotency-Key": "void-key"},
			wasInvoked: func(payments *paymentApplicationFake) bool { return payments.voidPaymentCalls == 1 },
		},
		{
			name:       "refund",
			path:       "/api/v1/payments/" + paymentID + "/refund",
			headers:    map[string]string{"Idempotency-Key": "refund-key"},
			wasInvoked: func(payments *paymentApplicationFake) bool { return payments.refundPaymentCalls == 1 },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Authorization succeeds but lacks payments:write, so the application must not observe it.
			t.Run("read-only credential is forbidden before the application", func(t *testing.T) {
				api := newPaymentAPITest(t)
				headers := make(map[string]string, len(tt.headers)+1)
				for key, value := range tt.headers {
					headers[key] = value
				}
				headers["Authorization"] = "Bearer read-credential"

				rec := api.request(t, http.MethodPost, tt.path, tt.body, headers)

				require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
				assert.Empty(t, rec.Header().Get("WWW-Authenticate"))
				assertErrorResponse(t, rec, "forbidden", http.StatusText(http.StatusForbidden))
				assert.False(t, tt.wasInvoked(api.payments))
			})

		})
	}
}

// Payment searches require a well-formed, valid bearer credential with payments:read; failed
// authentication must prevent the application query from running.
func TestPaymentReadsRequireBearerCredentialAndReadScope(t *testing.T) {
	for _, tt := range []struct {
		name, authorization string
		status              int
		code                string
		invoked             bool
	}{
		{"missing", "", http.StatusUnauthorized, "unauthorized", false},
		{"malformed", "Basic read-credential", http.StatusUnauthorized, "unauthorized", false},
		{"invalid", "Bearer invalid-credential", http.StatusUnauthorized, "unauthorized", false},
		{"valid read credential", "Bearer read-credential", http.StatusOK, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/payments?order_id=order-1", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()
			api.handler.ServeHTTP(rec, req)

			require.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
			if tt.status == http.StatusUnauthorized {
				assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
			}
			if tt.code != "" {
				assertErrorResponse(t, rec, tt.code, http.StatusText(tt.status))
			}
			assert.Equal(t, tt.invoked, api.payments.searchPaymentsQuery != (app.SearchPaymentsQuery{}))
		})
	}
}

// A recognized credential without payments:read is forbidden rather than challenged, and cannot
// execute either the payment search or lookup query.
func TestPaymentReadReturnsForbiddenForAuthenticatedCredentialWithoutReadScope(t *testing.T) {
	api := newPaymentAPITest(t)
	key := []byte("01234567890123456789012345678901")
	authenticator, err := serviceauth.NewAuthenticator(key, []serviceauth.Credential{{Digest: serviceauth.Digest(key, "write-only-credential"), Scopes: []serviceauth.Scope{serviceauth.ScopePaymentsWrite}}})
	require.NoError(t, err)
	config := testHandlerConfig(t)
	handler, err := httpapi.NewHandler(api.payments, api.readiness, discardLogger(), api.metrics, authenticator, &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, config)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments?order_id=order-1", nil)
	req.Header.Set("Authorization", "Bearer write-only-credential")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("WWW-Authenticate"))
	assertErrorResponse(t, rec, "forbidden", http.StatusText(http.StatusForbidden))
	assert.Equal(t, app.SearchPaymentsQuery{}, api.payments.searchPaymentsQuery)

	lookup := httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", nil)
	lookup.Header.Set("Authorization", "Bearer write-only-credential")
	lookupRec := httptest.NewRecorder()
	handler.ServeHTTP(lookupRec, lookup)
	require.Equal(t, http.StatusForbidden, lookupRec.Code)
	assertErrorResponse(t, lookupRec, "forbidden", http.StatusText(http.StatusForbidden))
	assert.Equal(t, app.GetPaymentQuery{}, api.payments.getPaymentQuery)
}

// Read and write traffic have separate rate-limit buckets; rejections happen before body parsing,
// and Retry-After rounds the next available token up to a whole second.
func TestPaymentRateLimitsUseIndependentRouteBucketsAndRoundRetryAfter(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	api := newRateLimitedPaymentAPITest(t, clock, httpapi.RateLimitConfig{ReadRequestsPerSecond: 3, ReadBurst: 1, WriteRequestsPerSecond: 1, WriteBurst: 1}, testAuthenticator(t))

	read := func() *httptest.ResponseRecorder {
		return api.request(t, http.MethodGet, "/api/v1/payments?order_id=order-1", "", nil)
	}
	write := func(body string) *httptest.ResponseRecorder {
		return api.request(t, http.MethodPost, "/api/v1/payments", body, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "replay-key"})
	}

	require.Equal(t, http.StatusOK, read().Code)
	firstRejection := read()
	require.Equal(t, http.StatusTooManyRequests, firstRejection.Code)
	assert.Equal(t, "1", firstRejection.Header().Get("Retry-After"))
	assertErrorResponse(t, firstRejection, "rate_limited", "rate limit exceeded")

	// Write capacity is independent of the exhausted read bucket.
	assert.NotEqual(t, http.StatusTooManyRequests, write(validAuthorizeBody()).Code)
	rejectedBeforeBodyParsing := write("not json")
	require.Equal(t, http.StatusTooManyRequests, rejectedBeforeBodyParsing.Code)
	assert.Equal(t, "1", rejectedBeforeBodyParsing.Header().Get("Retry-After"))
	assert.Equal(t, 1, api.payments.authorizePaymentCalls)

	clock.now = clock.now.Add(333 * time.Millisecond)
	require.Equal(t, http.StatusTooManyRequests, read().Code)
	clock.now = clock.now.Add(time.Millisecond)
	assert.Equal(t, http.StatusOK, read().Code)
	assert.Equal(t, []string{"read", "write", "read"}, api.metrics.rateLimitRejections)
}

// Operational probes remain available even after an API rate-limit bucket is exhausted.
func TestPaymentRateLimitsExcludeHealthAndReadinessEndpoints(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	api := newRateLimitedPaymentAPITest(t, clock, httpapi.RateLimitConfig{ReadRequestsPerSecond: 1, ReadBurst: 1, WriteRequestsPerSecond: 1, WriteBurst: 1}, testAuthenticator(t))

	require.Equal(t, http.StatusOK, api.request(t, http.MethodGet, "/api/v1/payments?order_id=order-1", "", nil).Code)
	require.Equal(t, http.StatusTooManyRequests, api.request(t, http.MethodGet, "/api/v1/payments?order_id=order-1", "", nil).Code)

	assert.Equal(t, http.StatusNoContent, api.request(t, http.MethodGet, "/healthz", "", nil).Code)
	assert.Equal(t, http.StatusNoContent, api.request(t, http.MethodGet, "/readyz", "", nil).Code)
}

// Only authorized requests consume quota. Credential rotation must not reset a principal's bucket,
// and a rejected request must not consume a token that becomes available later.
func TestPaymentRateLimitsDoNotChargeUnauthorizedOrForbiddenRequestsAndShareCredentialRotationQuota(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	authenticator, err := serviceauth.NewAuthenticator(key, []serviceauth.Credential{
		{Digest: serviceauth.Digest(key, "old-credential"), Scopes: []serviceauth.Scope{serviceauth.ScopePaymentsRead, serviceauth.ScopePaymentsWrite}},
		{Digest: serviceauth.Digest(key, "new-credential"), Scopes: []serviceauth.Scope{serviceauth.ScopePaymentsRead, serviceauth.ScopePaymentsWrite}},
		{Digest: serviceauth.Digest(key, "write-only-credential"), Scopes: []serviceauth.Scope{serviceauth.ScopePaymentsWrite}},
	})
	require.NoError(t, err)
	clock := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	api := newRateLimitedPaymentAPITest(t, clock, httpapi.RateLimitConfig{ReadRequestsPerSecond: 1, ReadBurst: 1, WriteRequestsPerSecond: 1, WriteBurst: 1}, authenticator)

	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(validAuthorizeBody()))
	unauthenticated.Header.Set("Content-Type", "application/json")
	unauthenticated.Header.Set("Idempotency-Key", "unauthenticated")
	unauthenticatedRec := httptest.NewRecorder()
	api.handler.ServeHTTP(unauthenticatedRec, unauthenticated)
	require.Equal(t, http.StatusUnauthorized, unauthenticatedRec.Code)

	forbidden := httptest.NewRequest(http.MethodGet, "/api/v1/payments?order_id=order-1", nil)
	forbidden.Header.Set("Authorization", "Bearer write-only-credential")
	forbiddenRec := httptest.NewRecorder()
	api.handler.ServeHTTP(forbiddenRec, forbidden)
	require.Equal(t, http.StatusForbidden, forbiddenRec.Code)

	oldCredential := api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{"Authorization": "Bearer old-credential", "Content-Type": "application/json", "Idempotency-Key": "same-operation"})
	assert.NotEqual(t, http.StatusTooManyRequests, oldCredential.Code)
	rotatedCredential := api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{"Authorization": "Bearer new-credential", "Content-Type": "application/json", "Idempotency-Key": "same-operation"})
	require.Equal(t, http.StatusTooManyRequests, rotatedCredential.Code)

	// The rejected replay did not consume a future token and can safely retry.
	clock.now = clock.now.Add(time.Second)
	assert.NotEqual(t, http.StatusTooManyRequests, api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{"Authorization": "Bearer new-credential", "Content-Type": "application/json", "Idempotency-Key": "same-operation"}).Code)
}

// A payment lookup rejects absent, malformed, or unknown credentials before it can issue a query.
func TestPaymentLookupRejectsUnauthenticatedRequestsBeforeApplication(t *testing.T) {
	for _, authorization := range []string{"", "Basic read-credential", "Bearer invalid-credential"} {
		t.Run(authorization, func(t *testing.T) {
			api := newPaymentAPITest(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", nil)
			if authorization != "" {
				req.Header.Set("Authorization", authorization)
			}
			rec := httptest.NewRecorder()
			api.handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
			assertErrorResponse(t, rec, "unauthorized", http.StatusText(http.StatusUnauthorized))
			assert.Equal(t, app.GetPaymentQuery{}, api.payments.getPaymentQuery)
		})
	}
}

// Handler construction fails closed when any runtime dependency is missing, so a partially wired
// HTTP server cannot start.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		new      func(*testing.T) (*httpapi.Handler, error)
	}{
		{
			name:     "payment application",
			expected: "httpapi handler: payment application is required",
			new: func(t *testing.T) (*httpapi.Handler, error) {
				return httpapi.NewHandler(nil, &readinessCheckerFake{}, discardLogger(), &recordingHTTPMetrics{}, testAuthenticator(t), &testClock{}, testHandlerConfig(t))
			},
		},
		{
			name:     "readiness checker",
			expected: "httpapi handler: readiness checker is required",
			new: func(t *testing.T) (*httpapi.Handler, error) {
				return httpapi.NewHandler(&paymentApplicationFake{}, nil, discardLogger(), &recordingHTTPMetrics{}, testAuthenticator(t), &testClock{}, testHandlerConfig(t))
			},
		},
		{
			name:     "logger",
			expected: "httpapi handler: logger is required",
			new: func(t *testing.T) (*httpapi.Handler, error) {
				return httpapi.NewHandler(&paymentApplicationFake{}, &readinessCheckerFake{}, nil, &recordingHTTPMetrics{}, testAuthenticator(t), &testClock{}, testHandlerConfig(t))
			},
		},
		{
			name:     "HTTP metrics recorder",
			expected: "httpapi handler: HTTP metrics recorder is required",
			new: func(t *testing.T) (*httpapi.Handler, error) {
				return httpapi.NewHandler(&paymentApplicationFake{}, &readinessCheckerFake{}, discardLogger(), nil, testAuthenticator(t), &testClock{}, testHandlerConfig(t))
			},
		},
		{
			name:     "service authenticator",
			expected: "httpapi handler: service authenticator is required",
			new: func(t *testing.T) (*httpapi.Handler, error) {
				return httpapi.NewHandler(&paymentApplicationFake{}, &readinessCheckerFake{}, discardLogger(), &recordingHTTPMetrics{}, nil, &testClock{}, testHandlerConfig(t))
			},
		},
		{
			name:     "rate limit clock",
			expected: "httpapi handler: rate limit clock is required",
			new: func(t *testing.T) (*httpapi.Handler, error) {
				return httpapi.NewHandler(&paymentApplicationFake{}, &readinessCheckerFake{}, discardLogger(), &recordingHTTPMetrics{}, testAuthenticator(t), nil, testHandlerConfig(t))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := tt.new(t)

			require.EqualError(t, err, tt.expected)
			assert.Nil(t, handler)
		})
	}
}

// Handler time budgets and body limits must be positive before the server accepts requests.
func TestNewHandlerRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*httpapi.HandlerConfig)
	}{
		{"payment command timeout", func(c *httpapi.HandlerConfig) { c.PaymentCommandTimeout = 0 }},
		{"payment read timeout", func(c *httpapi.HandlerConfig) { c.PaymentReadTimeout = 0 }},
		{"readiness timeout", func(c *httpapi.HandlerConfig) { c.ReadinessTimeout = 0 }},
		{"max request body bytes", func(c *httpapi.HandlerConfig) { c.MaxRequestBodyBytes = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := testHandlerConfig(t)
			tt.mutate(&config)
			handler, err := httpapi.NewHandler(&paymentApplicationFake{}, &readinessCheckerFake{}, discardLogger(), &recordingHTTPMetrics{}, testAuthenticator(t), &testClock{}, config)
			require.Nil(t, handler)
			require.Error(t, err)
		})
	}
}

// An authorization exceeding its command deadline returns the retry-safe payment-timeout response.
func TestPostPaymentsReturnsPaymentTimeoutWhenCommandDeadlineExpires(t *testing.T) {
	payments := &paymentApplicationFake{authorizePaymentFunc: func(ctx context.Context, _ app.AuthorizePaymentCommand) (app.PaymentCommandResult, error) {
		<-ctx.Done()
		return app.PaymentCommandResult{}, ctx.Err()
	}}
	config := testHandlerConfig(t)
	config.PaymentCommandTimeout = time.Millisecond
	handler, err := newTestHandler(t, payments, &readinessCheckerFake{}, discardLogger(), &recordingHTTPMetrics{}, config)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(validAuthorizeBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key")
	req.Header.Set("Authorization", "Bearer write-credential")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assertErrorResponse(t, rec, "payment_timeout", "payment command timed out; retry with the same idempotency key")
}

// Once the command deadline has expired, its payment-timeout response takes precedence over an
// underlying Mock Bank timeout.
func TestPostPaymentsPrefersPaymentTimeoutAfterMockBankTimeout(t *testing.T) {
	payments := &paymentApplicationFake{authorizePaymentFunc: func(ctx context.Context, _ app.AuthorizePaymentCommand) (app.PaymentCommandResult, error) {
		<-ctx.Done()
		return app.PaymentCommandResult{}, app.NewPaymentBankTimeoutError(ctx.Err())
	}}
	config := testHandlerConfig(t)
	config.PaymentCommandTimeout = time.Millisecond
	handler, err := newTestHandler(t, payments, &readinessCheckerFake{}, discardLogger(), &recordingHTTPMetrics{}, config)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(validAuthorizeBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key")
	req.Header.Set("Authorization", "Bearer write-credential")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assertErrorResponse(t, rec, "payment_timeout", "payment command timed out; retry with the same idempotency key")
}

// The authorization deadline starts before parsing the request body, preventing slow uploads from
// extending the command's allowed execution time.
func TestPostPaymentsCommandDeadlineIncludesRequestParsing(t *testing.T) {
	payments := &paymentApplicationFake{authorizePaymentFunc: func(ctx context.Context, _ app.AuthorizePaymentCommand) (app.PaymentCommandResult, error) {
		return app.PaymentCommandResult{}, nil
	}}
	config := testHandlerConfig(t)
	config.PaymentCommandTimeout = time.Millisecond
	handler, err := newTestHandler(t, payments, &readinessCheckerFake{}, discardLogger(), &recordingHTTPMetrics{}, config)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", &delayedReader{Reader: strings.NewReader(validAuthorizeBody()), Delay: 10 * time.Millisecond})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key")
	req.Header.Set("Authorization", "Bearer write-credential")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assertErrorResponse(t, rec, "payment_timeout", "payment command timed out; retry with the same idempotency key")
}

// Bodies above the configured limit are rejected before an authorization command is assembled or run.
func TestOversizedRequestBodyIsRejectedBeforePaymentCommand(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/api/v1/payments", strings.Repeat("x", 64*1024+1), map[string]string{
		"Content-Type": "application/json",
	})
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assertErrorResponse(t, rec, "request_body_too_large", "request body is too large")
	assert.Equal(t, app.AuthorizePaymentCommand{}, api.payments.authorizePaymentCommand)
}

// Both declared and streamed oversized bodies produce the same client error and are recorded as
// completed routed requests in logs and metrics.
func TestOversizedRequestBodyRecordsCompletionObservability(t *testing.T) {
	tests := []struct {
		name    string
		request func(*testing.T) *http.Request
	}{
		{
			name: "declared content length",
			request: func(t *testing.T) *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(oversizedJSONBody(t)))
			},
		},
		{
			name: "streamed body",
			request: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", nil)
				req.Body = io.NopCloser(strings.NewReader(oversizedJSONBody(t)))
				req.ContentLength = -1
				return req
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			api := newPaymentAPITestWithLogger(t, slog.New(slog.NewJSONHandler(&logs, nil)))
			req := tt.request(t)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer write-credential")
			rec := httptest.NewRecorder()

			api.handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, "request_body_too_large", "request body is too large")
			assert.Equal(t, app.AuthorizePaymentCommand{}, api.payments.authorizePaymentCommand)
			require.Len(t, api.metrics.requests, 1)
			assert.Equal(t, recordedHTTPRequest{
				method: http.MethodPost,
				route:  "/api/v1/payments",
				status: http.StatusRequestEntityTooLarge,
			}, api.metrics.requests[0].withoutDuration())

			var entry map[string]any
			require.NoError(t, json.Unmarshal(logs.Bytes(), &entry))
			assert.Equal(t, "http request", entry["msg"])
			assert.Equal(t, float64(http.StatusRequestEntityTooLarge), entry["status"])
		})
	}
}

// Body-size protection applies only after a gateway route is selected, preserving normal 404 and
// 405 routing outcomes for unmatched requests.
func TestOversizedRequestBodyUsesRoutingOutcomeWhenNoGatewayEndpointMatches(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{
			name:   "unknown path",
			method: http.MethodPost,
			path:   "/unknown",
			status: http.StatusNotFound,
		},
		{
			name:   "unsupported method",
			method: http.MethodPost,
			path:   "/healthz",
			status: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(oversizedJSONBody(t)))
			rec := httptest.NewRecorder()

			api.handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
			assert.Empty(t, api.metrics.requests)
		})
	}
}

func oversizedJSONBody(t *testing.T) string {
	t.Helper()
	return `{"order_id":"` + strings.Repeat("x", int(testHandlerConfig(t).MaxRequestBodyBytes)+1) + `"}`
}

// A payment read that outlives its dedicated deadline returns a request-timeout response.
func TestGetPaymentReturnsRequestTimeoutWhenReadDeadlineExpires(t *testing.T) {
	payments := &paymentApplicationFake{getPaymentFunc: func(ctx context.Context, _ app.GetPaymentQuery) (app.PaymentResult, error) {
		<-ctx.Done()
		return app.PaymentResult{}, ctx.Err()
	}}
	config := testHandlerConfig(t)
	config.PaymentReadTimeout = time.Millisecond
	handler, err := newTestHandler(t, payments, &readinessCheckerFake{}, discardLogger(), &recordingHTTPMetrics{}, config)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", nil)
	req.Header.Set("Authorization", "Bearer read-credential")
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assertErrorResponse(t, rec, "request_timeout", "payment read timed out")
}

// Authorization expiry is part of an authorized payment's public representation when the bank
// provides it.
func TestPostPaymentsReturnsAuthorizationExpirationWhenPresent(t *testing.T) {
	api := newPaymentAPITest(t)
	payment := newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	payment.AuthorizationExpiresAt = time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	api.payments.authorizePaymentResult = payment
	rec := api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "authorized",
			"authorization_expires_at": "2026-06-18T13:00:00Z",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:00:00Z"
		}
	}`, rec.Body.String())
}

// A declined authorization exposes its mapped decline reason and preserves the final payment state.
func TestPostPaymentsReturnsDeclinedPaymentWithDeclineReason(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.authorizePaymentResult = newDeclinedPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", rec.Header().Get("Location"))
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "declined",
			"decline_reason": "invalid_card",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:00:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
}

// A pending authorization returns 202 with a payment location, allowing callers to poll for the
// eventual outcome.
func TestPostPaymentsReturnsAcceptedPendingPaymentWithLocation(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.authorizePaymentFunc = func(context.Context, app.AuthorizePaymentCommand) (app.PaymentCommandResult, error) {
		return app.PaymentCommandResult{
			Payment: newPendingPayment("pay_550e8400-e29b-41d4-a716-446655440000"),
		}, nil
	}
	rec := api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	require.Equal(t, http.StatusAccepted, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", rec.Header().Get("Location"))
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "pending",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:00:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
}

// Retrying a pending authorization forwards the payment ID and idempotency key, then returns the
// newly authoritative payment representation.
func TestPostPaymentAuthorizationRetriesRetriesPendingAuthorization(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.retryAuthorizationResult = newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/authorization-retries", validRetryAuthorizationBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "retry-key-1",
	})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, mustRetryAuthorizationCommand(t), api.payments.retryAuthorizationCommand)
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "authorized",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:00:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
}

// Voiding an authorized payment invokes the matching command and returns its terminal state.
func TestPostPaymentVoidVoidsAuthorizedPayment(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.voidPaymentResult = newVoidedPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/void", "", map[string]string{
		"Idempotency-Key": "void-key-1",
	})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, mustVoidPaymentCommand(t, "pay_550e8400-e29b-41d4-a716-446655440000", "void-key-1"), api.payments.voidPaymentCommand)
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "voided",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:00:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
}

// Refunding a captured payment needs no request body; the endpoint forwards its path ID and key.
func TestPostPaymentRefundRefundsCapturedPaymentWithoutRequestBody(t *testing.T) {
	api := newPaymentAPITest(t)
	refunded := newRefundedPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	refunded.UpdatedAt = time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	api.payments.refundPaymentResult = refunded

	rec := api.request(t, http.MethodPost, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/refund", "", map[string]string{
		"Idempotency-Key": "public-refund-key-1",
	})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, mustRefundPaymentCommand(t, "pay_550e8400-e29b-41d4-a716-446655440000", "public-refund-key-1"), api.payments.refundPaymentCommand)
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "refunded",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T13:00:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
}

// A successful payment lookup returns the requested payment in the stable public envelope.
func TestGetPaymentByIDReturnsPayment(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.getPaymentResult = newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodGet, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", "", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, mustGetPaymentQuery(t, "pay_550e8400-e29b-41d4-a716-446655440000"), api.payments.getPaymentQuery)
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "authorized",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:00:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
	assert.NotContains(t, rec.Body.String(), "history")
}

// A missing payment is translated to the public 404 error rather than exposing application details.
func TestGetPaymentByIDMapsNotFound(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.getPaymentErr = app.NewPaymentNotFoundError("pay_550e8400-e29b-41d4-a716-446655440999", nil)
	rec := api.request(t, http.MethodGet, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440999", "", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "payment_not_found", "payment was not found")
}

// Search query parameters are translated into the application filter and the matching payments are
// returned in the public collection envelope.
func TestSearchPaymentsReturnsFilteredPayments(t *testing.T) {
	api := newPaymentAPITest(t)
	first := newPayment("pay_550e8400-e29b-41d4-a716-446655440001")
	second := newDeclinedPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	api.payments.searchPaymentsResult = []app.PaymentResult{first, second}
	rec := api.request(t, http.MethodGet, "/api/v1/payments?order_id=order-1&customer_id=customer-1&status=declined", "", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, mustSearchPaymentsQuery(t, "order-1", "customer-1", "declined"), api.payments.searchPaymentsQuery)
	assert.JSONEq(t, `{
		"payments": [
			{
				"id": "pay_550e8400-e29b-41d4-a716-446655440001",
				"order_id": "order-1",
				"customer_id": "customer-1",
				"amount": 1299,
				"currency": "USD",
				"status": "authorized",
				"created_at": "2026-06-18T12:00:00Z",
				"updated_at": "2026-06-18T12:00:00Z"
			},
			{
				"id": "pay_550e8400-e29b-41d4-a716-446655440000",
				"order_id": "order-1",
				"customer_id": "customer-1",
				"amount": 1299,
				"currency": "USD",
				"status": "declined",
				"decline_reason": "invalid_card",
				"created_at": "2026-06-18T12:00:00Z",
				"updated_at": "2026-06-18T12:00:00Z"
			}
		]
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
	assert.NotContains(t, rec.Body.String(), "history")
}

// Unsupported search filters fail validation before the payment search is invoked.
func TestSearchPaymentsRejectsUnsupportedFilters(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "unknown query parameter", path: "/api/v1/payments?order_id=order-1&limit=10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			rec := api.request(t, http.MethodGet, tt.path, "", nil)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, "validation_error", "payment request is invalid")
			assert.Zero(t, api.payments.searchPaymentsQuery)
		})
	}
}

// Commands with JSON payloads accept application/json (including parameters) and reject missing or
// incompatible media types before parsing the body.
func TestJSONPaymentCommandsRequireJSONContentType(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		body    string
		headers map[string]string
	}{
		{
			name:    "authorize",
			path:    "/api/v1/payments",
			body:    validAuthorizeBody(),
			headers: map[string]string{"Idempotency-Key": "public-key-1"},
		},
		{
			name:    "authorization retry",
			path:    "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/authorization-retries",
			body:    validRetryAuthorizationBody(),
			headers: map[string]string{"Idempotency-Key": "retry-key-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			rec := api.request(t, http.MethodPost, tt.path, tt.body, tt.headers)

			assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, "unsupported_media_type", "content type must be application/json")
		})
	}
}

// Malformed JSON produces a stable invalid-body response and does not call the authorizer.
func TestPostPaymentsRejectsMalformedJSON(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/api/v1/payments", `{"order_id":`, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "invalid_json_body", "invalid JSON body")
}

// Unknown JSON fields are rejected so client typos cannot be silently ignored.
func TestPostPaymentsRejectsUnknownFields(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/api/v1/payments", `{"order_id":"order-1","unexpected":true}`, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "invalid_json_body", "invalid JSON body")
}

// Validation and idempotency errors from the application map to their stable client-facing status,
// code, and safe message; unexpected errors remain internal.
func TestPostPaymentsMapsValidationAndMissingIdempotencyErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		code    string
		message string
		status  int
	}{
		{name: "invalid input", err: app.NewInvalidPaymentInputError("amount must be greater than zero", nil), code: "validation_error", message: "payment request is invalid", status: http.StatusUnprocessableEntity},
		{name: "payment not found", err: app.NewPaymentNotFoundError("pay_123", nil), code: "payment_not_found", message: "payment was not found", status: http.StatusNotFound},
		{name: "idempotency conflict", err: app.NewPaymentIdempotencyConflictError(nil), code: "idempotency_key_conflict", message: "idempotency key was already used with a different request", status: http.StatusConflict},
		{name: "idempotency in progress", err: app.NewPaymentIdempotencyInProgressError(nil), code: "idempotency_key_in_progress", message: "idempotency key is already in progress", status: http.StatusConflict},
		{name: "payment status conflict", err: app.NewPaymentStatusConflictError(nil), code: "payment_status_conflict", message: "payment status does not allow this operation", status: http.StatusConflict},
		{name: "bank unavailable", err: app.NewPaymentBankUnavailableError(errors.New("connection refused")), code: "bank_unavailable", message: "bank is unavailable", status: http.StatusBadGateway},
		{name: "bank state conflict", err: app.NewPaymentBankStateConflictError(errors.New("already captured")), code: "bank_state_conflict", message: "bank state conflicts with local payment state", status: http.StatusBadGateway},
		{name: "bank timeout", err: app.NewPaymentBankTimeoutError(context.DeadlineExceeded), code: "bank_timeout", message: "bank request timed out", status: http.StatusGatewayTimeout},
		{name: "internal", err: app.NewInternalPaymentError(errors.New("scan failed")), code: "internal_server_error", message: "internal server error", status: http.StatusInternalServerError},
		{name: "wrapped payment error", err: fmt.Errorf("authorize payment: %w", app.NewPaymentBankUnavailableError(errors.New("connection refused"))), code: "bank_unavailable", message: "bank is unavailable", status: http.StatusBadGateway},
		{name: "raw error", err: errors.New("raw failure"), code: "internal_server_error", message: "Internal Server Error", status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			api.payments.authorizePaymentErr = tt.err
			rec := api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{
				"Content-Type":    "application/json",
				"Idempotency-Key": "public-key-1",
			})

			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, tt.code, tt.message)
		})
	}
}

// Capturing uses the path payment ID and idempotency key without a body, then returns the captured
// payment without bank-internal fields.
func TestPostPaymentCaptureCapturesPaymentWithoutRequestBody(t *testing.T) {
	api := newPaymentAPITest(t)
	captured := newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	captured.Status = "captured"
	captured.UpdatedAt = time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	api.payments.capturePaymentResult = captured

	rec := api.request(t, http.MethodPost, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/capture", "", map[string]string{
		"Idempotency-Key": "public-capture-key-1",
	})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Header().Get("Location"))
	assert.Equal(t, mustCapturePaymentCommand(t, "pay_550e8400-e29b-41d4-a716-446655440000", "public-capture-key-1"), api.payments.capturePaymentCommand)
	assert.JSONEq(t, `{
		"payment": {
			"id": "pay_550e8400-e29b-41d4-a716-446655440000",
			"order_id": "order-1",
			"customer_id": "customer-1",
			"amount": 1299,
			"currency": "USD",
			"status": "captured",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:30:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
}

// Capture requests reject even whitespace-only bodies so this bodyless endpoint has one unambiguous contract.
func TestPostPaymentCaptureRejectsRequestBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "json object", body: `{}`},
		{name: "whitespace", body: " \n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			rec := api.request(t, http.MethodPost, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/capture", tt.body, map[string]string{
				"Content-Type":    "application/json",
				"Idempotency-Key": "public-capture-key-1",
			})

			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, "invalid_json_body", "request body must be empty")
			assert.Zero(t, api.payments.capturePaymentCommand)
		})
	}
}

// Refund requests are likewise bodyless; any supplied bytes fail before the command runs.
func TestPostPaymentRefundRejectsRequestBody(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/refund", `{}`, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-refund-key-1",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "invalid_json_body", "request body must be empty")
	assert.Zero(t, api.payments.refundPaymentCommand)
}

// A panic in application code becomes a safe 500, closes the connection, and is still visible in
// HTTP metrics rather than escaping the server.
func TestPostPaymentsRecoversPanic(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.authorizePaymentPanic = "database pool exploded"
	rec := api.request(t, http.MethodPost, "/api/v1/payments", validAuthorizeBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "close", rec.Header().Get("Connection"))
	assertErrorResponse(t, rec, "internal_server_error", "Internal Server Error")
	require.Len(t, api.metrics.requests, 1)
	assert.Equal(t, recordedHTTPRequest{
		method: http.MethodPost,
		route:  "/api/v1/payments",
		status: http.StatusInternalServerError,
	}, api.metrics.requests[0].withoutDuration())
}

// Liveness is independent of dependencies: a healthy process returns an empty 204 response.
func TestHealthzReturnsNoContent(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodGet, "/healthz", "", nil)

	assert.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Body.String())
}

// Readiness delegates to its dependency check and signals success with an empty 204 response.
func TestReadyzReturnsNoContentWhenPostgresIsReady(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodGet, "/readyz", "", nil)

	assert.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Body.String())
	assert.True(t, api.readiness.checked)
}

// A failed readiness dependency makes the process unavailable to load balancers with a safe 503.
func TestReadyzReturnsUnavailableWhenPostgresIsNotReady(t *testing.T) {
	api := newPaymentAPITest(t)
	api.readiness.err = errors.New("postgres unavailable")
	rec := api.request(t, http.MethodGet, "/readyz", "", nil)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "service_unavailable", "Service Unavailable")
}

// Request metrics use the route template, not a concrete payment ID, to keep metric cardinality bounded.
func TestServerRecordsHTTPMetricsWithRoutePattern(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.getPaymentResult = newPayment("pay_550e8400-e29b-41d4-a716-446655440000")

	rec := api.request(t, http.MethodGet, "/api/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", "", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, api.metrics.requests, 1)
	assert.Equal(t, recordedHTTPRequest{
		method: http.MethodGet,
		route:  "/api/v1/payments/{payment_id}",
		status: http.StatusOK,
	}, api.metrics.requests[0].withoutDuration())
}

// Health and readiness requests are also observable, each under its own stable route label.
func TestServerRecordsHTTPMetricsForOperationalEndpoints(t *testing.T) {
	api := newPaymentAPITest(t)

	health := api.request(t, http.MethodGet, "/healthz", "", nil)
	ready := api.request(t, http.MethodGet, "/readyz", "", nil)

	require.Equal(t, http.StatusNoContent, health.Code, "body: %s", health.Body.String())
	require.Equal(t, http.StatusNoContent, ready.Code, "body: %s", ready.Body.String())
	require.Len(t, api.metrics.requests, 2)
	assert.Equal(t, "/healthz", api.metrics.requests[0].route)
	assert.Equal(t, "/readyz", api.metrics.requests[1].route)
}

// The public listener deliberately does not expose /metrics; metrics belong to the separate
// operational listener.
func TestMetricsEndpointIsNotServedByThePublicHandler(t *testing.T) {
	payments := &paymentApplicationFake{}
	readiness := &readinessCheckerFake{}
	metrics := &recordingHTTPMetrics{}
	handler, err := newTestHandler(t, payments, readiness, discardLogger(), metrics, testHandlerConfig(t))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, metrics.requests)
}

type delayedReader struct {
	io.Reader
	Delay   time.Duration
	delayed bool
}

func (r *delayedReader) Read(p []byte) (int, error) {
	if !r.delayed {
		r.delayed = true
		time.Sleep(r.Delay)
	}
	return r.Reader.Read(p)
}

func validAuthorizeBody() string {
	return `{
		"order_id": "order-1",
		"customer_id": "customer-1",
		"amount": 1299,
		"card": {
			"number": "4111111111111111",
			"cvv": "123",
			"expiry_month": 12,
			"expiry_year": 2030
		}
	}`
}

func validRetryAuthorizationBody() string {
	return `{
		"card": {
			"number": "4111111111111111",
			"cvv": "123",
			"expiry_month": 12,
			"expiry_year": 2030
		}
	}`
}

type paymentAPITest struct {
	payments  *paymentApplicationFake
	readiness *readinessCheckerFake
	handler   http.Handler
	metrics   *recordingHTTPMetrics
}

func newPaymentAPITest(t *testing.T) *paymentAPITest {
	t.Helper()

	return newPaymentAPITestWithLogger(t, discardLogger())
}

func newPaymentAPITestWithLogger(t *testing.T, logger *slog.Logger) *paymentAPITest {
	t.Helper()

	payments := &paymentApplicationFake{}
	readiness := &readinessCheckerFake{}
	metrics := &recordingHTTPMetrics{}

	handler, err := newTestHandler(t, payments, readiness, logger, metrics, testHandlerConfig(t))
	require.NoError(t, err)

	return &paymentAPITest{
		payments:  payments,
		readiness: readiness,
		handler:   handler,
		metrics:   metrics,
	}
}

func (api *paymentAPITest) request(t *testing.T, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if req.Header.Get("Authorization") == "" {
		if method == http.MethodGet {
			req.Header.Set("Authorization", "Bearer read-credential")
		} else {
			req.Header.Set("Authorization", "Bearer write-credential")
		}
	}
	rec := httptest.NewRecorder()

	api.handler.ServeHTTP(rec, req)

	return rec
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var body T
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return body
}

func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, code, message string) {
	t.Helper()

	body := decodeJSON[errorResponse](t, rec)
	assert.Equal(t, code, body.Error.Code)
	assert.Equal(t, message, body.Error.Message)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testHandlerConfig(t *testing.T) httpapi.HandlerConfig {
	t.Helper()
	return httpapi.HandlerConfig{
		PaymentCommandTimeout: time.Second,
		PaymentReadTimeout:    time.Second,
		ReadinessTimeout:      time.Second,
		MaxRequestBodyBytes:   64 * 1024,
		RateLimit:             httpapi.RateLimitConfig{ReadRequestsPerSecond: 30, ReadBurst: 60, WriteRequestsPerSecond: 5, WriteBurst: 10},
	}
}

func newTestHandler(t *testing.T, payments *paymentApplicationFake, readiness *readinessCheckerFake, logger *slog.Logger, metrics *recordingHTTPMetrics, config httpapi.HandlerConfig) (*httpapi.Handler, error) {
	t.Helper()
	return httpapi.NewHandler(payments, readiness, logger, metrics, testAuthenticator(t), &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, config)
}

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func newRateLimitedPaymentAPITest(t *testing.T, clock *testClock, rateLimitConfig httpapi.RateLimitConfig, authenticator *serviceauth.Authenticator) *paymentAPITest {
	t.Helper()
	handlerConfig := testHandlerConfig(t)
	handlerConfig.RateLimit = rateLimitConfig
	payments := &paymentApplicationFake{}
	readiness := &readinessCheckerFake{}
	metrics := &recordingHTTPMetrics{}
	handler, err := httpapi.NewHandler(payments, readiness, discardLogger(), metrics, authenticator, clock, handlerConfig)
	require.NoError(t, err)
	return &paymentAPITest{payments: payments, readiness: readiness, handler: handler, metrics: metrics}
}

func testAuthenticator(t *testing.T) *serviceauth.Authenticator {
	t.Helper()
	key := []byte("01234567890123456789012345678901")
	authenticator, err := serviceauth.NewAuthenticator(key, []serviceauth.Credential{
		{Digest: serviceauth.Digest(key, "read-credential"), Scopes: []serviceauth.Scope{serviceauth.ScopePaymentsRead}},
		{Digest: serviceauth.Digest(key, "write-credential"), Scopes: []serviceauth.Scope{serviceauth.ScopePaymentsRead, serviceauth.ScopePaymentsWrite}},
	})
	require.NoError(t, err)
	return authenticator
}

type recordingHTTPMetrics struct {
	requests            []recordedHTTPRequest
	rateLimitRejections []string
}

func (m *recordingHTTPMetrics) RecordHTTPRequest(method string, route string, status int, duration time.Duration) {
	m.requests = append(m.requests, recordedHTTPRequest{
		method:   method,
		route:    route,
		status:   status,
		duration: duration,
	})
}

func (m *recordingHTTPMetrics) RecordRateLimitRejection(routeClass string) {
	m.rateLimitRejections = append(m.rateLimitRejections, routeClass)
}

type recordedHTTPRequest struct {
	method   string
	route    string
	status   int
	duration time.Duration
}

func (r recordedHTTPRequest) withoutDuration() recordedHTTPRequest {
	r.duration = 0
	return r
}

type paymentApplicationFake struct {
	authorizePaymentFunc      func(context.Context, app.AuthorizePaymentCommand) (app.PaymentCommandResult, error)
	authorizePaymentCalls     int
	authorizePaymentCommand   app.AuthorizePaymentCommand
	authorizePaymentResult    app.PaymentResult
	authorizePaymentErr       error
	authorizePaymentPanic     any
	retryAuthorizationCommand app.RetryAuthorizationCommand
	retryAuthorizationCalls   int
	retryAuthorizationResult  app.PaymentResult
	retryAuthorizationErr     error
	retryAuthorizationPanic   any
	capturePaymentCommand     app.CapturePaymentCommand
	capturePaymentCalls       int
	capturePaymentResult      app.PaymentResult
	capturePaymentErr         error
	capturePaymentPanic       any
	voidPaymentCommand        app.VoidPaymentCommand
	voidPaymentCalls          int
	voidPaymentResult         app.PaymentResult
	voidPaymentErr            error
	refundPaymentCommand      app.RefundPaymentCommand
	refundPaymentCalls        int
	refundPaymentResult       app.PaymentResult
	refundPaymentErr          error
	getPaymentQuery           app.GetPaymentQuery
	getPaymentResult          app.PaymentResult
	getPaymentErr             error
	getPaymentFunc            func(context.Context, app.GetPaymentQuery) (app.PaymentResult, error)
	searchPaymentsQuery       app.SearchPaymentsQuery
	searchPaymentsResult      []app.PaymentResult
	searchPaymentsErr         error
}

type readinessCheckerFake struct {
	checked bool
	err     error
}

func (f *readinessCheckerFake) CheckReady(context.Context) error {
	f.checked = true
	return f.err
}

func (f *paymentApplicationFake) AuthorizePayment(ctx context.Context, command app.AuthorizePaymentCommand) (app.PaymentCommandResult, error) {
	f.authorizePaymentCalls++
	if f.authorizePaymentFunc != nil {
		return f.authorizePaymentFunc(ctx, command)
	}
	if f.authorizePaymentPanic != nil {
		panic(f.authorizePaymentPanic)
	}
	f.authorizePaymentCommand = command
	return app.PaymentCommandResult{Payment: f.authorizePaymentResult}, f.authorizePaymentErr
}

func (f *paymentApplicationFake) RetryAuthorization(_ context.Context, command app.RetryAuthorizationCommand) (app.PaymentCommandResult, error) {
	f.retryAuthorizationCalls++
	if f.retryAuthorizationPanic != nil {
		panic(f.retryAuthorizationPanic)
	}
	f.retryAuthorizationCommand = command
	return app.PaymentCommandResult{Payment: f.retryAuthorizationResult}, f.retryAuthorizationErr
}

func (f *paymentApplicationFake) CapturePayment(_ context.Context, command app.CapturePaymentCommand) (app.PaymentCommandResult, error) {
	f.capturePaymentCalls++
	if f.capturePaymentPanic != nil {
		panic(f.capturePaymentPanic)
	}
	f.capturePaymentCommand = command
	return app.PaymentCommandResult{Payment: f.capturePaymentResult}, f.capturePaymentErr
}

func (f *paymentApplicationFake) VoidPayment(_ context.Context, command app.VoidPaymentCommand) (app.PaymentCommandResult, error) {
	f.voidPaymentCalls++
	f.voidPaymentCommand = command
	return app.PaymentCommandResult{Payment: f.voidPaymentResult}, f.voidPaymentErr
}

func (f *paymentApplicationFake) RefundPayment(_ context.Context, command app.RefundPaymentCommand) (app.PaymentCommandResult, error) {
	f.refundPaymentCalls++
	f.refundPaymentCommand = command
	return app.PaymentCommandResult{Payment: f.refundPaymentResult}, f.refundPaymentErr
}

func (f *paymentApplicationFake) GetPayment(ctx context.Context, query app.GetPaymentQuery) (app.PaymentResult, error) {
	if f.getPaymentFunc != nil {
		return f.getPaymentFunc(ctx, query)
	}
	f.getPaymentQuery = query
	return f.getPaymentResult, f.getPaymentErr
}

func (f *paymentApplicationFake) SearchPayments(_ context.Context, query app.SearchPaymentsQuery) ([]app.PaymentResult, error) {
	f.searchPaymentsQuery = query
	return f.searchPaymentsResult, f.searchPaymentsErr
}

func mustAuthorizePaymentCommand(t *testing.T) app.AuthorizePaymentCommand {
	t.Helper()
	command, err := app.NewAuthorizePaymentCommand("order-1", "customer-1", 1299, "4111111111111111", "123", 12, 2030, "public-key-1")
	require.NoError(t, err)
	return command
}

func mustRetryAuthorizationCommand(t *testing.T) app.RetryAuthorizationCommand {
	t.Helper()
	command, err := app.NewRetryAuthorizationCommand("pay_550e8400-e29b-41d4-a716-446655440000", "4111111111111111", "123", 12, 2030, "retry-key-1")
	require.NoError(t, err)
	return command
}

func mustCapturePaymentCommand(t *testing.T, paymentID string, idempotencyKey string) app.CapturePaymentCommand {
	t.Helper()
	command, err := app.NewCapturePaymentCommand(paymentID, idempotencyKey)
	require.NoError(t, err)
	return command
}

func mustVoidPaymentCommand(t *testing.T, paymentID string, idempotencyKey string) app.VoidPaymentCommand {
	t.Helper()
	command, err := app.NewVoidPaymentCommand(paymentID, idempotencyKey)
	require.NoError(t, err)
	return command
}

func mustRefundPaymentCommand(t *testing.T, paymentID string, idempotencyKey string) app.RefundPaymentCommand {
	t.Helper()
	command, err := app.NewRefundPaymentCommand(paymentID, idempotencyKey)
	require.NoError(t, err)
	return command
}

func mustGetPaymentQuery(t *testing.T, paymentID string) app.GetPaymentQuery {
	t.Helper()
	query, err := app.NewGetPaymentQuery(paymentID)
	require.NoError(t, err)
	return query
}

func mustSearchPaymentsQuery(t *testing.T, orderID string, customerID string, status string) app.SearchPaymentsQuery {
	t.Helper()
	query, err := app.NewSearchPaymentsQuery(orderID, customerID, status)
	require.NoError(t, err)
	return query
}

func newPayment(id string) app.PaymentResult {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	return app.PaymentResult{
		ID:          id,
		OrderID:     "order-1",
		CustomerID:  "customer-1",
		AmountCents: 1299,
		Currency:    "USD",
		Status:      "authorized",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func newDeclinedPayment(id string) app.PaymentResult {
	payment := newPayment(id)
	payment.Status = "declined"
	payment.DeclineReason = "invalid_card"
	return payment
}

func newPendingPayment(id string) app.PaymentResult {
	payment := newPayment(id)
	payment.Status = string(domain.PaymentStatusPending)
	return payment
}

func newVoidedPayment(id string) app.PaymentResult {
	payment := newPayment(id)
	payment.Status = "voided"
	return payment
}

func newRefundedPayment(id string) app.PaymentResult {
	payment := newPayment(id)
	payment.Status = "refunded"
	return payment
}

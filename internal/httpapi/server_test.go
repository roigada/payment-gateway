package httpapi_test

import (
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
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostPaymentsAuthorizesPayment(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.authorizePaymentResult = newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/v1/payments", validAuthorizeBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", rec.Header().Get("Location"))
	assert.Equal(t, app.AuthorizePaymentCommand{
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		AmountCents:    1299,
		IdempotencyKey: "public-key-1",
		Card: app.CardDetails{
			Number:      "4111111111111111",
			CVV:         "123",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		},
	}, api.payments.authorizePaymentCommand)
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

func TestPostPaymentsReturnsDeclinedPaymentWithDeclineReason(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.authorizePaymentResult = newDeclinedPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/v1/payments", validAuthorizeBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", rec.Header().Get("Location"))
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

func TestPostPaymentsReturnsPendingPayment(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.authorizePaymentResult = newPendingPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/v1/payments", validAuthorizeBody(), map[string]string{
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
			"status": "pending",
			"created_at": "2026-06-18T12:00:00Z",
			"updated_at": "2026-06-18T12:00:00Z"
		}
	}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bank")
}

func TestPostPaymentAuthorizationRetriesRetriesPendingAuthorization(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.retryAuthorizationResult = newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/authorization-retries", validRetryAuthorizationBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "retry-key-1",
	})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, app.RetryAuthorizationCommand{
		PaymentID:      "pay_550e8400-e29b-41d4-a716-446655440000",
		IdempotencyKey: "retry-key-1",
		Card: app.CardDetails{
			Number:      "4111111111111111",
			CVV:         "123",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		},
	}, api.payments.retryAuthorizationCommand)
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

func TestPostPaymentVoidVoidsAuthorizedPayment(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.voidPaymentResult = newVoidedPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/void", "", map[string]string{
		"Idempotency-Key": "void-key-1",
	})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, app.VoidPaymentCommand{
		PaymentID:      "pay_550e8400-e29b-41d4-a716-446655440000",
		IdempotencyKey: "void-key-1",
	}, api.payments.voidPaymentCommand)
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

func TestPostPaymentRefundRefundsCapturedPaymentWithoutRequestBody(t *testing.T) {
	api := newPaymentAPITest(t)
	refunded := newRefundedPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	refunded.UpdatedAt = time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	api.payments.refundPaymentResult = refunded

	rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/refund", "", map[string]string{
		"Idempotency-Key": "public-refund-key-1",
	})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, app.RefundPaymentCommand{
		PaymentID:      "pay_550e8400-e29b-41d4-a716-446655440000",
		IdempotencyKey: "public-refund-key-1",
	}, api.payments.refundPaymentCommand)
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

func TestGetPaymentByIDReturnsPayment(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.getPaymentResult = newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	rec := api.request(t, http.MethodGet, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000", "", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, app.GetPaymentQuery{
		PaymentID: "pay_550e8400-e29b-41d4-a716-446655440000",
	}, api.payments.getPaymentQuery)
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

func TestGetPaymentByIDMapsNotFound(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.getPaymentErr = app.NewPaymentNotFound("pay_missing", nil)
	rec := api.request(t, http.MethodGet, "/v1/payments/not-a-payment-id", "", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "payment_not_found", "payment was not found")
}

func TestSearchPaymentsReturnsFilteredPayments(t *testing.T) {
	api := newPaymentAPITest(t)
	first := newPayment("pay_550e8400-e29b-41d4-a716-446655440001")
	second := newDeclinedPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	api.payments.searchPaymentsResult = []app.PaymentResult{first, second}
	rec := api.request(t, http.MethodGet, "/v1/payments?order_id=order-1&customer_id=customer-1&status=declined", "", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, app.SearchPaymentsQuery{
		OrderID:    "order-1",
		CustomerID: "customer-1",
		Status:     "declined",
	}, api.payments.searchPaymentsQuery)
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

func TestSearchPaymentsRejectsUnsupportedFilters(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "unfiltered", path: "/v1/payments"},
		{name: "status only", path: "/v1/payments?status=authorized"},
		{name: "unknown query parameter", path: "/v1/payments?order_id=order-1&limit=10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			api.payments.searchPaymentsErr = app.NewInvalidPaymentInput("order id or customer id is required", nil)
			rec := api.request(t, http.MethodGet, tt.path, "", nil)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, "validation_error", "payment request is invalid")
		})
	}
}

func TestPostPaymentsRequiresJSONContentType(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/payments", validAuthorizeBody(), map[string]string{
		"Idempotency-Key": "public-key-1",
	})

	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "unsupported_media_type", "content type must be application/json")
}

func TestPostPaymentAuthorizationRetriesRequiresJSONContentType(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/authorization-retries", validRetryAuthorizationBody(), map[string]string{
		"Idempotency-Key": "retry-key-1",
	})

	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "unsupported_media_type", "content type must be application/json")
}

func TestPostPaymentsRejectsMalformedJSON(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/payments", `{"order_id":`, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "invalid_json_body", "invalid JSON body")
}

func TestPostPaymentsRejectsUnknownFields(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/payments", `{"order_id":"order-1","unexpected":true}`, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "invalid_json_body", "invalid JSON body")
}

func TestPostPaymentsMapsValidationAndMissingIdempotencyErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		code    string
		message string
		status  int
	}{
		{name: "invalid input", err: app.NewInvalidPaymentInput("amount must be greater than zero", nil), code: "validation_error", message: "payment request is invalid", status: http.StatusUnprocessableEntity},
		{name: "payment not found", err: app.NewPaymentNotFound("pay_123", nil), code: "payment_not_found", message: "payment was not found", status: http.StatusNotFound},
		{name: "idempotency conflict", err: app.NewPaymentIdempotencyConflict(nil), code: "idempotency_key_conflict", message: "idempotency key was already used with a different request", status: http.StatusConflict},
		{name: "invalid status conflict", err: app.NewPaymentInvalidStatusConflict(nil), code: "payment_status_conflict", message: "payment status does not allow this operation", status: http.StatusConflict},
		{name: "bank unavailable", err: app.NewPaymentBankUnavailable(errors.New("connection refused")), code: "bank_unavailable", message: "bank is unavailable", status: http.StatusBadGateway},
		{name: "bank timeout", err: app.NewPaymentBankTimeout(context.DeadlineExceeded), code: "bank_timeout", message: "bank request timed out", status: http.StatusGatewayTimeout},
		{name: "internal", err: app.NewInternalPaymentError(errors.New("scan failed")), code: "internal_server_error", message: "internal server error", status: http.StatusInternalServerError},
		{name: "wrapped payment error", err: fmt.Errorf("authorize payment: %w", app.NewPaymentBankUnavailable(errors.New("connection refused"))), code: "bank_unavailable", message: "bank is unavailable", status: http.StatusBadGateway},
		{name: "raw error", err: errors.New("raw failure"), code: "internal_server_error", message: "Internal Server Error", status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			api.payments.authorizePaymentErr = tt.err
			rec := api.request(t, http.MethodPost, "/v1/payments", validAuthorizeBody(), map[string]string{
				"Content-Type":    "application/json",
				"Idempotency-Key": "public-key-1",
			})

			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, tt.code, tt.message)
		})
	}
}

func TestPostPaymentCaptureCapturesPaymentWithoutRequestBody(t *testing.T) {
	api := newPaymentAPITest(t)
	captured := newPayment("pay_550e8400-e29b-41d4-a716-446655440000")
	captured.Status = "captured"
	captured.UpdatedAt = time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC)
	api.payments.capturePaymentResult = captured

	rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/capture", "", map[string]string{
		"Idempotency-Key": "public-capture-key-1",
	})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Header().Get("Location"))
	assert.Equal(t, app.CapturePaymentCommand{
		PaymentID:      "pay_550e8400-e29b-41d4-a716-446655440000",
		IdempotencyKey: "public-capture-key-1",
	}, api.payments.capturePaymentCommand)
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
			rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/capture", tt.body, map[string]string{
				"Content-Type":    "application/json",
				"Idempotency-Key": "public-capture-key-1",
			})

			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, "invalid_json_body", "request body must be empty")
			assert.Zero(t, api.payments.capturePaymentCommand)
		})
	}
}

func TestPostPaymentRefundRejectsRequestBody(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/refund", `{}`, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-refund-key-1",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "invalid_json_body", "request body must be empty")
	assert.Zero(t, api.payments.refundPaymentCommand)
}

func TestPostPaymentCaptureMapsPaymentErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		code    string
		message string
		status  int
	}{
		{name: "invalid input", err: app.NewInvalidPaymentInput("idempotency key is required", nil), code: "validation_error", message: "payment request is invalid", status: http.StatusUnprocessableEntity},
		{name: "payment not found", err: app.NewPaymentNotFound("pay_123", nil), code: "payment_not_found", message: "payment was not found", status: http.StatusNotFound},
		{name: "invalid transition", err: app.NewPaymentInvalidStatusConflict(nil), code: "payment_status_conflict", message: "payment status does not allow this operation", status: http.StatusConflict},
		{name: "idempotency conflict", err: app.NewPaymentIdempotencyConflict(nil), code: "idempotency_key_conflict", message: "idempotency key was already used with a different request", status: http.StatusConflict},
		{name: "bank unavailable", err: app.NewPaymentBankUnavailable(errors.New("connection refused")), code: "bank_unavailable", message: "bank is unavailable", status: http.StatusBadGateway},
		{name: "bank timeout", err: app.NewPaymentBankTimeout(context.DeadlineExceeded), code: "bank_timeout", message: "bank request timed out", status: http.StatusGatewayTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			api.payments.capturePaymentErr = tt.err
			rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/capture", "", map[string]string{
				"Idempotency-Key": "public-capture-key-1",
			})

			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, tt.code, tt.message)
		})
	}
}

func TestPostPaymentRefundMapsPaymentErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		code    string
		message string
		status  int
	}{
		{name: "invalid input", err: app.NewInvalidPaymentInput("idempotency key is required", nil), code: "validation_error", message: "payment request is invalid", status: http.StatusUnprocessableEntity},
		{name: "payment not found", err: app.NewPaymentNotFound("pay_123", nil), code: "payment_not_found", message: "payment was not found", status: http.StatusNotFound},
		{name: "invalid transition", err: app.NewPaymentInvalidStatusConflict(nil), code: "payment_status_conflict", message: "payment status does not allow this operation", status: http.StatusConflict},
		{name: "idempotency conflict", err: app.NewPaymentIdempotencyConflict(nil), code: "idempotency_key_conflict", message: "idempotency key was already used with a different request", status: http.StatusConflict},
		{name: "bank unavailable", err: app.NewPaymentBankUnavailable(errors.New("connection refused")), code: "bank_unavailable", message: "bank is unavailable", status: http.StatusBadGateway},
		{name: "bank timeout", err: app.NewPaymentBankTimeout(context.DeadlineExceeded), code: "bank_timeout", message: "bank request timed out", status: http.StatusGatewayTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newPaymentAPITest(t)
			api.payments.refundPaymentErr = tt.err
			rec := api.request(t, http.MethodPost, "/v1/payments/pay_550e8400-e29b-41d4-a716-446655440000/refund", "", map[string]string{
				"Idempotency-Key": "public-refund-key-1",
			})

			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
			assertErrorResponse(t, rec, tt.code, tt.message)
		})
	}
}

func TestPostPaymentsRecoversPanic(t *testing.T) {
	api := newPaymentAPITest(t)
	api.payments.authorizePaymentPanic = "database pool exploded"
	rec := api.request(t, http.MethodPost, "/v1/payments", validAuthorizeBody(), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "public-key-1",
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "close", rec.Header().Get("Connection"))
	assertErrorResponse(t, rec, "internal_server_error", "Internal Server Error")
}

func TestHealthzReturnsNoContent(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodGet, "/healthz", "", nil)

	assert.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Body.String())
}

func TestReadyzReturnsNoContentWhenPostgresIsReady(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodGet, "/readyz", "", nil)

	assert.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Body.String())
	assert.True(t, api.readiness.checked)
}

func TestReadyzReturnsUnavailableWhenPostgresIsNotReady(t *testing.T) {
	api := newPaymentAPITest(t)
	api.readiness.err = errors.New("postgres unavailable")
	rec := api.request(t, http.MethodGet, "/readyz", "", nil)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "service_unavailable", "Service Unavailable")
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
	payments  *paymentUseCasesFake
	readiness *readinessCheckerFake
	handler   http.Handler
}

func newPaymentAPITest(t *testing.T) *paymentAPITest {
	t.Helper()

	payments := &paymentUseCasesFake{}
	readiness := &readinessCheckerFake{}

	return &paymentAPITest{
		payments:  payments,
		readiness: readiness,
		handler:   httpapi.NewServer(payments, readiness, discardLogger()),
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

type paymentUseCasesFake struct {
	authorizePaymentCommand   app.AuthorizePaymentCommand
	authorizePaymentResult    app.PaymentResult
	authorizePaymentErr       error
	authorizePaymentPanic     any
	retryAuthorizationCommand app.RetryAuthorizationCommand
	retryAuthorizationResult  app.PaymentResult
	retryAuthorizationErr     error
	retryAuthorizationPanic   any
	capturePaymentCommand     app.CapturePaymentCommand
	capturePaymentResult      app.PaymentResult
	capturePaymentErr         error
	capturePaymentPanic       any
	voidPaymentCommand        app.VoidPaymentCommand
	voidPaymentResult         app.PaymentResult
	voidPaymentErr            error
	refundPaymentCommand      app.RefundPaymentCommand
	refundPaymentResult       app.PaymentResult
	refundPaymentErr          error
	getPaymentQuery           app.GetPaymentQuery
	getPaymentResult          app.PaymentResult
	getPaymentErr             error
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

func (f *paymentUseCasesFake) AuthorizePayment(_ context.Context, command app.AuthorizePaymentCommand) (app.PaymentResult, error) {
	if f.authorizePaymentPanic != nil {
		panic(f.authorizePaymentPanic)
	}
	f.authorizePaymentCommand = command
	return f.authorizePaymentResult, f.authorizePaymentErr
}

func (f *paymentUseCasesFake) RetryAuthorization(_ context.Context, command app.RetryAuthorizationCommand) (app.PaymentResult, error) {
	if f.retryAuthorizationPanic != nil {
		panic(f.retryAuthorizationPanic)
	}
	f.retryAuthorizationCommand = command
	return f.retryAuthorizationResult, f.retryAuthorizationErr
}

func (f *paymentUseCasesFake) CapturePayment(_ context.Context, command app.CapturePaymentCommand) (app.PaymentResult, error) {
	if f.capturePaymentPanic != nil {
		panic(f.capturePaymentPanic)
	}
	f.capturePaymentCommand = command
	return f.capturePaymentResult, f.capturePaymentErr
}

func (f *paymentUseCasesFake) VoidPayment(_ context.Context, command app.VoidPaymentCommand) (app.PaymentResult, error) {
	f.voidPaymentCommand = command
	return f.voidPaymentResult, f.voidPaymentErr
}

func (f *paymentUseCasesFake) RefundPayment(_ context.Context, command app.RefundPaymentCommand) (app.PaymentResult, error) {
	f.refundPaymentCommand = command
	return f.refundPaymentResult, f.refundPaymentErr
}

func (f *paymentUseCasesFake) GetPayment(_ context.Context, query app.GetPaymentQuery) (app.PaymentResult, error) {
	f.getPaymentQuery = query
	return f.getPaymentResult, f.getPaymentErr
}

func (f *paymentUseCasesFake) SearchPayments(_ context.Context, query app.SearchPaymentsQuery) ([]app.PaymentResult, error) {
	f.searchPaymentsQuery = query
	return f.searchPaymentsResult, f.searchPaymentsErr
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
	payment.Status = "pending"
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

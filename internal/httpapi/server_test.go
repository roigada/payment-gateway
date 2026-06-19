package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
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

func TestPostPaymentsRequiresJSONContentType(t *testing.T) {
	api := newPaymentAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/payments", validAuthorizeBody(), map[string]string{
		"Idempotency-Key": "public-key-1",
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
		{name: "invalid order id", err: domain.ErrInvalidOrderID, code: "invalid_order_id", message: "invalid order id", status: http.StatusUnprocessableEntity},
		{name: "invalid customer id", err: domain.ErrInvalidCustomerID, code: "invalid_customer_id", message: "invalid customer id", status: http.StatusUnprocessableEntity},
		{name: "invalid amount", err: domain.ErrInvalidAmount, code: "invalid_amount", message: "invalid amount", status: http.StatusUnprocessableEntity},
		{name: "invalid card details", err: app.ErrInvalidCardDetails, code: "invalid_card_details", message: "invalid card details", status: http.StatusUnprocessableEntity},
		{name: "missing idempotency key", err: app.ErrMissingIdempotencyKey, code: "missing_idempotency_key", message: "missing idempotency key", status: http.StatusUnprocessableEntity},
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
	authorizePaymentCommand app.AuthorizePaymentCommand
	authorizePaymentResult  app.PaymentResult
	authorizePaymentErr     error
	authorizePaymentPanic   any
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

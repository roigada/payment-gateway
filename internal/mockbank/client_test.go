package mockbank_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/roigada/payment-gateway/internal/mockbank"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorizePaymentSendsBankPayloadAndOperationKey(t *testing.T) {
	var gotPath string
	var gotIdempotencyKey string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"authorization_id": "auth_550e8400-e29b-41d4-a716-446655440000",
			"status": "approved",
			"amount": 1299,
			"currency": "USD",
			"expires_at": "2026-06-18T16:00:00Z",
			"created_at": "2026-06-18T15:00:00Z"
		}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	result, err := client.AuthorizePayment(context.Background(), app.BankAuthorizationRequest{
		OperationKey: "bok_123",
		OrderID:      "order-1",
		CustomerID:   "customer-1",
		AmountCents:  1299,
		Currency:     "USD",
		Card: app.CardDetails{
			Number:      "4111111111111111",
			CVV:         "123",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		},
	})
	require.NoError(t, err)

	assert.Equal(t, app.BankAuthorizationResult{BankAuthorizationID: "auth_550e8400-e29b-41d4-a716-446655440000"}, result)
	assert.Equal(t, "/api/v1/authorizations", gotPath)
	assert.Equal(t, "bok_123", gotIdempotencyKey)
	assert.Equal(t, map[string]any{
		"card_number":  "4111111111111111",
		"cvv":          "123",
		"expiry_month": float64(12),
		"expiry_year":  float64(2030),
		"amount":       float64(1299),
	}, gotBody)
}

func TestAuthorizePaymentRejectsMalformedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorization_id":""}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), app.BankAuthorizationRequest{
		OperationKey: "bok_123",
		OrderID:      "order-1",
		CustomerID:   "customer-1",
		AmountCents:  1299,
		Currency:     "USD",
		Card: app.CardDetails{
			Number:      "4111111111111111",
			CVV:         "123",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		},
	})

	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
}

func TestAuthorizePaymentMapsInsufficientFundsToGatewayDeclineReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"insufficient_funds","message":"not enough funds"}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	result, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
	require.NoError(t, err)

	assert.Equal(t, app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInsufficientFunds}, result)
}

func TestAuthorizePaymentMapsBankValidationFailuresToInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "invalid amount",
			body: `{"error":"invalid_amount","message":"amount must be positive"}`,
		},
		{
			name: "invalid card",
			body: `{"error":"invalid_card","message":"invalid card"}`,
		},
		{
			name: "invalid cvv",
			body: `{"error":"invalid_cvv","message":"invalid cvv"}`,
		},
		{
			name: "expired card",
			body: `{"error":"card_expired","message":"expired card"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := mockbank.NewClient(server.URL, server.Client())
			require.NoError(t, err)

			_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

			require.Error(t, err)
			assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInvalidInput))
		})
	}
}

func TestAuthorizePaymentReturnsErrorForBankFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "non-decline bad request",
			status: http.StatusBadRequest,
			body:   `{"error":"unknown_error","message":"unknown"}`,
		},
		{
			name:   "internal bank error",
			status: http.StatusInternalServerError,
			body:   `{"error":"internal_error","message":"try again"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := mockbank.NewClient(server.URL, server.Client())
			require.NoError(t, err)

			_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

			require.Error(t, err)
			assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
		})
	}
}

func TestAuthorizePaymentMapsBankTimeoutToTimeoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.Timeout = time.Nanosecond
	client, err := mockbank.NewClient(server.URL, httpClient)
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

	require.Error(t, err)
	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

func TestAuthorizePaymentMapsTransportTimeoutToTimeoutError(t *testing.T) {
	client, err := mockbank.NewClient("http://mockbank.example", &http.Client{Transport: timeoutRoundTripper{}})
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

	require.Error(t, err)
	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

type timeoutRoundTripper struct{}

func (timeoutRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, timeoutError{}
}

type timeoutError struct{}

func (timeoutError) Error() string {
	return "transport timed out"
}

func (timeoutError) Timeout() bool {
	return true
}

func (timeoutError) Temporary() bool {
	return true
}

func validAuthorizationRequest() app.BankAuthorizationRequest {
	return app.BankAuthorizationRequest{
		OperationKey: "bok_123",
		OrderID:      "order-1",
		CustomerID:   "customer-1",
		AmountCents:  1299,
		Currency:     "USD",
		Card: app.CardDetails{
			Number:      "4111111111111111",
			CVV:         "123",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		},
	}
}

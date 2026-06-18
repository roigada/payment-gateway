package mockbank_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roigada/payment-gateway/internal/app"
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

	assert.Equal(t, app.BankAuthorizationResult{AuthorizationReference: "auth_550e8400-e29b-41d4-a716-446655440000"}, result)
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
		w.WriteHeader(http.StatusCreated)
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

	assert.Error(t, err)
}

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
		OperationKey:    "bok_123",
		OrderID:         "order-1",
		CustomerID:      "customer-1",
		AmountCents:     1299,
		Currency:        "USD",
		CardNumber:      "4111111111111111",
		CardCVV:         "123",
		CardExpiryMonth: 12,
		CardExpiryYear:  2030,
	})
	require.NoError(t, err)

	assert.Equal(t, app.BankAuthorizationResult{
		BankAuthorizationID:    "auth_550e8400-e29b-41d4-a716-446655440000",
		AuthorizationExpiresAt: time.Date(2026, 6, 18, 16, 0, 0, 0, time.UTC),
	}, result)
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
		OperationKey:    "bok_123",
		OrderID:         "order-1",
		CustomerID:      "customer-1",
		AmountCents:     1299,
		Currency:        "USD",
		CardNumber:      "4111111111111111",
		CardCVV:         "123",
		CardExpiryMonth: 12,
		CardExpiryYear:  2030,
	})

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
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
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
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
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
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
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

func TestClientRecordsMockBankRequestMetrics(t *testing.T) {
	tests := []struct {
		name      string
		call      func(*mockbank.Client) error
		handler   http.HandlerFunc
		operation string
		result    string
	}{
		{
			name: "authorization success",
			call: func(client *mockbank.Client) error {
				_, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{
					"authorization_id": "auth_550e8400-e29b-41d4-a716-446655440000",
					"expires_at": "2026-06-18T16:00:00Z"
				}`))
			},
			operation: "authorize",
			result:    "success",
		},
		{
			name: "authorization decline",
			call: func(client *mockbank.Client) error {
				_, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = w.Write([]byte(`{"error":"insufficient_funds","message":"not enough funds"}`))
			},
			operation: "authorize",
			result:    "declined",
		},
		{
			name: "capture expired",
			call: func(client *mockbank.Client) error {
				_, err := client.CapturePayment(context.Background(), validCaptureRequest())
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_expired","message":"authorization expired"}`))
			},
			operation: "capture",
			result:    "expired",
		},
		{
			name: "void state conflict",
			call: func(client *mockbank.Client) error {
				_, err := client.VoidPayment(context.Background(), validVoidRequest())
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"already_voided","message":"already voided"}`))
			},
			operation: "void",
			result:    "state_conflict",
		},
		{
			name: "refund invalid input",
			call: func(client *mockbank.Client) error {
				_, err := client.RefundPayment(context.Background(), validRefundRequest())
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"capture_not_found","message":"capture not found"}`))
			},
			operation: "refund",
			result:    "invalid_input",
		},
		{
			name: "authorization unavailable",
			call: func(client *mockbank.Client) error {
				_, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal_error","message":"try again"}`))
			},
			operation: "authorize",
			result:    "unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			client, err := mockbank.NewClient(server.URL, server.Client())
			require.NoError(t, err)
			metrics := &recordingMockBankMetrics{}
			client.SetMetrics(metrics)

			_ = tt.call(client)

			require.Len(t, metrics.requests, 1)
			assert.Equal(t, tt.operation, metrics.requests[0].operation)
			assert.Equal(t, tt.result, metrics.requests[0].result)
			assert.Positive(t, metrics.requests[0].duration)
		})
	}
}

func TestClientRecordsTimeoutMetric(t *testing.T) {
	client, err := mockbank.NewClient("http://mockbank.example", &http.Client{Transport: timeoutRoundTripper{}})
	require.NoError(t, err)
	metrics := &recordingMockBankMetrics{}
	client.SetMetrics(metrics)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

	require.Error(t, err)
	require.Len(t, metrics.requests, 1)
	assert.Equal(t, "authorize", metrics.requests[0].operation)
	assert.Equal(t, "timeout", metrics.requests[0].result)
	assert.Positive(t, metrics.requests[0].duration)
}

func TestAuthorizePaymentMapsTransportTimeoutToTimeoutError(t *testing.T) {
	client, err := mockbank.NewClient("http://mockbank.example", &http.Client{Transport: timeoutRoundTripper{}})
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

func TestCapturePaymentSendsBankPayloadAndOperationKey(t *testing.T) {
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
			"capture_id": "cap_550e8400-e29b-41d4-a716-446655440001",
			"authorization_id": "auth_550e8400-e29b-41d4-a716-446655440000",
			"status": "captured",
			"amount": 1299,
			"currency": "USD",
			"captured_at": "2026-06-18T15:00:00Z"
		}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	result, err := client.CapturePayment(context.Background(), validCaptureRequest())
	require.NoError(t, err)

	assert.Equal(t, app.BankCaptureResult{BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001"}, result)
	assert.Equal(t, "/api/v1/captures", gotPath)
	assert.Equal(t, "bok_123", gotIdempotencyKey)
	assert.Equal(t, map[string]any{
		"authorization_id": "auth_550e8400-e29b-41d4-a716-446655440000",
		"amount":           float64(1299),
	}, gotBody)
}

func TestCapturePaymentRejectsMalformedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"capture_id":""}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	_, err = client.CapturePayment(context.Background(), validCaptureRequest())

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
}

func TestCapturePaymentReturnsErrorForBankFailures(t *testing.T) {
	tests := []struct {
		name string
		code string
		kind app.PaymentErrorKind
	}{
		{name: "amount mismatch", code: "amount_mismatch", kind: app.PaymentErrorInvalidInput},
		{name: "authorization not found", code: "authorization_not_found", kind: app.PaymentErrorInvalidInput},
		{name: "authorization already used", code: "authorization_already_used", kind: app.PaymentErrorBankStateConflict},
		{name: "already captured", code: "already_captured", kind: app.PaymentErrorBankStateConflict},
		{name: "internal", code: "internal_error", kind: app.PaymentErrorBankUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"` + tt.code + `","message":"capture failed"}`))
			}))
			defer server.Close()

			client, err := mockbank.NewClient(server.URL, server.Client())
			require.NoError(t, err)

			_, err = client.CapturePayment(context.Background(), validCaptureRequest())

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
		})
	}
}

func TestCapturePaymentMapsExpiredAuthorizationToLifecycleError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_expired","message":"authorization expired"}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	_, err = client.CapturePayment(context.Background(), validCaptureRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))
}

func TestCapturePaymentMapsBankTimeoutToTimeoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.Timeout = time.Nanosecond
	client, err := mockbank.NewClient(server.URL, httpClient)
	require.NoError(t, err)

	_, err = client.CapturePayment(context.Background(), validCaptureRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

func TestVoidPaymentSendsBankPayloadAndOperationKey(t *testing.T) {
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
			"void_id": "void_550e8400-e29b-41d4-a716-446655440002",
			"authorization_id": "auth_550e8400-e29b-41d4-a716-446655440000",
			"status": "voided",
			"voided_at": "2026-06-18T15:00:00Z"
		}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	result, err := client.VoidPayment(context.Background(), validVoidRequest())
	require.NoError(t, err)

	assert.Equal(t, app.BankVoidResult{BankVoidID: "void_550e8400-e29b-41d4-a716-446655440002"}, result)
	assert.Equal(t, "/api/v1/voids", gotPath)
	assert.Equal(t, "bok_123", gotIdempotencyKey)
	assert.Equal(t, map[string]any{
		"authorization_id": "auth_550e8400-e29b-41d4-a716-446655440000",
	}, gotBody)
}

func TestVoidPaymentRejectsMalformedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"void_id":""}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	_, err = client.VoidPayment(context.Background(), validVoidRequest())

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
}

func TestVoidPaymentReturnsErrorForBankFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   app.PaymentErrorKind
	}{
		{
			name:   "bad request",
			status: http.StatusBadRequest,
			body:   `{"error":"already_voided","message":"already voided"}`,
			kind:   app.PaymentErrorBankStateConflict,
		},
		{
			name:   "internal bank error",
			status: http.StatusInternalServerError,
			body:   `{"error":"internal_error","message":"try again"}`,
			kind:   app.PaymentErrorBankUnavailable,
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

			_, err = client.VoidPayment(context.Background(), validVoidRequest())

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
		})
	}
}

func TestVoidPaymentMapsExpiredAuthorizationToLifecycleError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_expired","message":"authorization expired"}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	_, err = client.VoidPayment(context.Background(), validVoidRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))
}

func TestVoidPaymentMapsBankTimeoutToTimeoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.Timeout = time.Nanosecond
	client, err := mockbank.NewClient(server.URL, httpClient)
	require.NoError(t, err)

	_, err = client.VoidPayment(context.Background(), validVoidRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

func TestRefundPaymentSendsBankPayloadAndOperationKey(t *testing.T) {
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
			"refund_id": "ref_550e8400-e29b-41d4-a716-446655440003",
			"capture_id": "cap_550e8400-e29b-41d4-a716-446655440001",
			"status": "refunded",
			"amount": 1299,
			"currency": "USD",
			"refunded_at": "2026-06-18T15:00:00Z"
		}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	result, err := client.RefundPayment(context.Background(), validRefundRequest())
	require.NoError(t, err)

	assert.Equal(t, app.BankRefundResult{BankRefundID: "ref_550e8400-e29b-41d4-a716-446655440003"}, result)
	assert.Equal(t, "/api/v1/refunds", gotPath)
	assert.Equal(t, "bok_123", gotIdempotencyKey)
	assert.Equal(t, map[string]any{
		"capture_id": "cap_550e8400-e29b-41d4-a716-446655440001",
		"amount":     float64(1299),
	}, gotBody)
}

func TestRefundPaymentRejectsMalformedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"refund_id":""}`))
	}))
	defer server.Close()

	client, err := mockbank.NewClient(server.URL, server.Client())
	require.NoError(t, err)

	_, err = client.RefundPayment(context.Background(), validRefundRequest())

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
}

func TestRefundPaymentReturnsErrorForBankFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   app.PaymentErrorKind
	}{
		{
			name:   "capture not found",
			status: http.StatusBadRequest,
			body:   `{"error":"capture_not_found","message":"capture not found"}`,
			kind:   app.PaymentErrorInvalidInput,
		},
		{
			name:   "already refunded",
			status: http.StatusBadRequest,
			body:   `{"error":"already_refunded","message":"already refunded"}`,
			kind:   app.PaymentErrorBankStateConflict,
		},
		{
			name:   "internal bank error",
			status: http.StatusInternalServerError,
			body:   `{"error":"internal_error","message":"try again"}`,
			kind:   app.PaymentErrorBankUnavailable,
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

			_, err = client.RefundPayment(context.Background(), validRefundRequest())

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
		})
	}
}

func TestRefundPaymentMapsBankTimeoutToTimeoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.Timeout = time.Nanosecond
	client, err := mockbank.NewClient(server.URL, httpClient)
	require.NoError(t, err)

	_, err = client.RefundPayment(context.Background(), validRefundRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
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

type recordingMockBankMetrics struct {
	requests []recordedMockBankRequest
}

func (m *recordingMockBankMetrics) RecordMockBankRequest(operation string, result string, duration time.Duration) {
	m.requests = append(m.requests, recordedMockBankRequest{
		operation: operation,
		result:    result,
		duration:  duration,
	})
}

type recordedMockBankRequest struct {
	operation string
	result    string
	duration  time.Duration
}

func validAuthorizationRequest() app.BankAuthorizationRequest {
	return app.BankAuthorizationRequest{
		OperationKey:    "bok_123",
		OrderID:         "order-1",
		CustomerID:      "customer-1",
		AmountCents:     1299,
		Currency:        "USD",
		CardNumber:      "4111111111111111",
		CardCVV:         "123",
		CardExpiryMonth: 12,
		CardExpiryYear:  2030,
	}
}

func validCaptureRequest() app.BankCaptureRequest {
	return app.BankCaptureRequest{
		OperationKey:        "bok_123",
		BankAuthorizationID: "auth_550e8400-e29b-41d4-a716-446655440000",
		AmountCents:         1299,
		Currency:            "USD",
	}
}

func validVoidRequest() app.BankVoidRequest {
	return app.BankVoidRequest{
		OperationKey:        "bok_123",
		BankAuthorizationID: "auth_550e8400-e29b-41d4-a716-446655440000",
	}
}

func validRefundRequest() app.BankRefundRequest {
	return app.BankRefundRequest{
		OperationKey:  "bok_123",
		BankCaptureID: "cap_550e8400-e29b-41d4-a716-446655440001",
		AmountCents:   1299,
		Currency:      "USD",
	}
}

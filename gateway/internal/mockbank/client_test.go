package mockbank

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that new client builds configured http transport.
func TestNewClientBuildsConfiguredHTTPTransport(t *testing.T) {
	config := retryConfig()
	config.BaseURL = url.URL{Scheme: "https", Host: "mockbank.example"}
	config.TLSHandshakeTimeout = 2
	config.ResponseHeaderTimeout = 3
	config.IdleConnectionTimeout = 4
	client, err := NewClient(noopMockBankMetrics{}, config)

	require.NoError(t, err)
	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, time.Duration(2), transport.TLSHandshakeTimeout)
	assert.Equal(t, time.Duration(3), transport.ResponseHeaderTimeout)
	assert.Equal(t, time.Duration(4), transport.IdleConnTimeout)
}

// Verifies that new http request builds mock bank request.
func TestNewHTTPRequestBuildsMockBankRequest(t *testing.T) {
	config := retryConfig()
	config.BaseURL = url.URL{Scheme: "https", Host: "mockbank.example"}
	client, err := NewClient(noopMockBankMetrics{}, config)
	require.NoError(t, err)

	request, err := client.newHTTPRequest(context.Background(), "/api/v1/authorizations", &bytes.Buffer{}, "bok_123")

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, request.Method)
	assert.Equal(t, "https://mockbank.example/api/v1/authorizations", request.URL.String())
	assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
	assert.Equal(t, "bok_123", request.Header.Get("Idempotency-Key"))
}

// Verifies that new client rejects nil metrics.
func TestNewClientRejectsNilMetrics(t *testing.T) {
	client, err := NewClient(nil, Config{BaseURL: url.URL{Scheme: "https", Host: "mockbank.example"}})

	require.Nil(t, client)
	require.EqualError(t, err, "mock bank metrics are required")
}

// Verifies that new client rejects invalid config.
func TestNewClientRejectsInvalidConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"base URL", func(config *Config) { config.BaseURL = url.URL{Path: "relative"} }, "mock bank base URL must be absolute"},
		{"initial attempt timeout", func(config *Config) { config.InitialAttemptTimeout = 0 }, "mock bank initial attempt timeout must be positive"},
		{"retry delay", func(config *Config) { config.RetryDelay = 0 }, "mock bank retry delay must be positive"},
		{"retry attempt timeout", func(config *Config) { config.RetryAttemptTimeout = 0 }, "mock bank retry attempt timeout must be positive"},
		{"connect timeout", func(config *Config) { config.ConnectTimeout = 0 }, "mock bank connect timeout must be positive"},
		{"TLS handshake timeout", func(config *Config) { config.TLSHandshakeTimeout = 0 }, "mock bank TLS handshake timeout must be positive"},
		{"response header timeout", func(config *Config) { config.ResponseHeaderTimeout = 0 }, "mock bank response header timeout must be positive"},
		{"idle connection timeout", func(config *Config) { config.IdleConnectionTimeout = 0 }, "mock bank idle connection timeout must be positive"},
	} {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			config := retryConfig()
			config.BaseURL = url.URL{Scheme: "https", Host: "mockbank.example"}
			tt.mutate(&config)

			client, err := NewClient(noopMockBankMetrics{}, config)

			require.Nil(t, client)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

// Verifies that decode bad request code normalizes bank code.
func TestDecodeBadRequestCodeNormalizesBankCode(t *testing.T) {
	code, err := decodeBadRequestCode(&http.Response{
		Body: io.NopCloser(strings.NewReader(`{"error":" Invalid_Card "}`)),
	})

	require.NoError(t, err)
	assert.Equal(t, "invalid_card", code)
}

// Verifies that decode bad request code returns raw decode failure.
func TestDecodeBadRequestCodeReturnsRawDecodeFailure(t *testing.T) {
	_, err := decodeBadRequestCode(&http.Response{
		Body: io.NopCloser(strings.NewReader(`{`)),
	})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "bank is unavailable")
}

// Verifies that authorize payment sends bank payload and operation key.
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

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
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

// Verifies that authorize payment rejects malformed success response.
func TestAuthorizePaymentRejectsMalformedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorization_id":""}`))
	}))
	defer server.Close()

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
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

// Verifies that authorize payment maps insufficient funds to gateway decline reason.
func TestAuthorizePaymentMapsInsufficientFundsToGatewayDeclineReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"insufficient_funds","message":"not enough funds"}`))
	}))
	defer server.Close()

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
	require.NoError(t, err)

	result, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
	require.NoError(t, err)

	assert.Equal(t, app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInsufficientFunds}, result)
}

// Verifies that authorize payment maps bank validation failures to invalid input.
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
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
			require.NoError(t, err)

			_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
		})
	}
}

// Verifies that authorize payment maps expired card to gateway decline reason.
func TestAuthorizePaymentMapsExpiredCardToGatewayDeclineReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"card_expired","message":"expired card"}`))
	}))
	defer server.Close()

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
	require.NoError(t, err)

	result, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
	require.NoError(t, err)
	assert.Equal(t, domain.DeclineReasonExpiredCard, result.DeclineReason)
}

// Verifies that authorize payment returns error for bank failures.
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
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
			require.NoError(t, err)

			_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
		})
	}
}

// Verifies that authorize payment maps bank timeout to timeout error.
func TestAuthorizePaymentMapsBankTimeoutToTimeoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.Timeout = time.Nanosecond
	client, err := testClientWithHTTPClient(server.URL, httpClient, noopMockBankMetrics{}, retryConfig())
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

// Verifies that client records mock bank request metrics.
func TestClientRecordsMockBankRequestMetrics(t *testing.T) {
	tests := []struct {
		name      string
		call      func(*Client) error
		handler   http.HandlerFunc
		operation string
		result    string
	}{
		{
			name: "authorization success",
			call: func(client *Client) error {
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
			call: func(client *Client) error {
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
			call: func(client *Client) error {
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
			call: func(client *Client) error {
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
			name: "refund state conflict",
			call: func(client *Client) error {
				_, err := client.RefundPayment(context.Background(), validRefundRequest())
				return err
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"capture_not_found","message":"capture not found"}`))
			},
			operation: "refund",
			result:    "state_conflict",
		},
		{
			name: "authorization unavailable",
			call: func(client *Client) error {
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
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			metrics := &recordingMockBankMetrics{}
			client, err := testClientWithHTTPClient(server.URL, server.Client(), metrics, retryConfig())
			require.NoError(t, err)

			_ = tt.call(client)

			if tt.result == "unavailable" {
				require.Len(t, metrics.requests, 2)
			} else {
				require.Len(t, metrics.requests, 1)
			}
			assert.Equal(t, tt.operation, metrics.requests[0].operation)
			assert.Equal(t, tt.result, metrics.requests[0].result)
			assert.Positive(t, metrics.requests[0].duration)
		})
	}
}

// Verifies that client records timeout metric.
func TestClientRecordsTimeoutMetric(t *testing.T) {
	metrics := &recordingMockBankMetrics{}
	client, err := testClientWithHTTPClient("http://mockbank.example", &http.Client{Transport: timeoutRoundTripper{}}, metrics, retryConfig())
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

	require.Error(t, err)
	require.Len(t, metrics.requests, 2)
	assert.Equal(t, "authorize", metrics.requests[0].operation)
	assert.Equal(t, "timeout", metrics.requests[0].result)
	assert.Positive(t, metrics.requests[0].duration)
}

// Verifies that authorize payment maps transport timeout to timeout error.
func TestAuthorizePaymentMapsTransportTimeoutToTimeoutError(t *testing.T) {
	client, err := testClientWithHTTPClient("http://mockbank.example", &http.Client{Transport: timeoutRoundTripper{}}, noopMockBankMetrics{}, retryConfig())
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

// Verifies that authorize payment retries transient failure with same operation key.
func TestAuthorizePaymentRetriesTransientFailureWithSameOperationKey(t *testing.T) {
	var operationKeys []string
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		operationKeys = append(operationKeys, r.Header.Get("Idempotency-Key"))
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorization_id":"auth_550e8400-e29b-41d4-a716-446655440000","expires_at":"2026-06-18T16:00:00Z"}`))
	}))
	defer server.Close()

	metrics := &recordingMockBankMetrics{}
	client, err := testClientWithHTTPClient(server.URL, server.Client(), metrics, retryConfig())
	require.NoError(t, err)

	result, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
	require.NoError(t, err)
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", result.BankAuthorizationID)
	assert.Equal(t, []string{"bok_123", "bok_123"}, operationKeys)
	require.Len(t, metrics.requests, 2)
	assert.Equal(t, []string{"unavailable", "success"}, []string{metrics.requests[0].result, metrics.requests[1].result})
	assert.Equal(t, []recordedMockBankRetry{{operation: "authorize", result: "attempted"}, {operation: "authorize", result: "succeeded"}}, metrics.retries)
}

// Verifies that authorize payment retries timeout and preserves exhaustion error.
func TestAuthorizePaymentRetriesTimeoutAndPreservesExhaustionError(t *testing.T) {
	transport := &timeoutThenTimeoutRoundTripper{}
	metrics := &recordingMockBankMetrics{}
	client, err := testClientWithHTTPClient("http://mockbank.example", &http.Client{Transport: transport}, metrics, retryConfig())
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())
	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
	assert.Equal(t, 2, transport.calls)
	require.Len(t, metrics.requests, 2)
	assert.Equal(t, []recordedMockBankRetry{{operation: "authorize", result: "attempted"}, {operation: "authorize", result: "failed"}}, metrics.retries)
}

// Verifies that authorize payment retries timeout then succeeds.
func TestAuthorizePaymentRetriesTimeoutThenSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorization_id":"auth_550e8400-e29b-41d4-a716-446655440000","expires_at":"2026-06-18T16:00:00Z"}`))
	}))
	defer server.Close()

	transport := &timeoutThenSuccessRoundTripper{next: http.DefaultTransport}
	client, err := testClientWithHTTPClient(server.URL, &http.Client{Transport: transport}, noopMockBankMetrics{}, retryConfig())
	require.NoError(t, err)

	result, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
	require.NoError(t, err)
	assert.Equal(t, "auth_550e8400-e29b-41d4-a716-446655440000", result.BankAuthorizationID)
	assert.Equal(t, 2, transport.calls)
}

// Verifies that authorize payment does not retry definitive outcome.
func TestAuthorizePaymentDoesNotRetryDefinitiveOutcome(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer server.Close()

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
	require.NoError(t, err)

	result, err := client.AuthorizePayment(context.Background(), validAuthorizationRequest())
	require.NoError(t, err)
	assert.Equal(t, domain.DeclineReasonInsufficientFunds, result.DeclineReason)
	assert.Equal(t, 1, attempts)
}

// Verifies that authorize payment cancellation stops retry delay.
func TestAuthorizePaymentCancellationStopsRetryDelay(t *testing.T) {
	firstAttempt := make(chan struct{})
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		close(firstAttempt)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	metrics := &recordingMockBankMetrics{}
	config := retryConfig()
	config.RetryDelay = time.Second
	client, err := testClientWithHTTPClient(server.URL, server.Client(), metrics, config)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.AuthorizePayment(ctx, validAuthorizationRequest())
		result <- err
	}()
	select {
	case <-firstAttempt:
		cancel()
	case <-time.After(time.Second):
		require.FailNow(t, "first attempt did not complete")
	}
	select {
	case err := <-result:
		require.Error(t, err)
		assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
	case <-time.After(100 * time.Millisecond):
		require.FailNow(t, "retry delay was not cancelled")
	}
	assert.Equal(t, 1, attempts)
	assert.Empty(t, metrics.retries)
}

// Verifies that authorize payment command deadline stops retry delay.
func TestAuthorizePaymentCommandDeadlineStopsRetryDelay(t *testing.T) {
	firstAttempt := make(chan struct{})
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		close(firstAttempt)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	config := retryConfig()
	config.RetryDelay = time.Second
	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, config)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = client.AuthorizePayment(ctx, validAuthorizationRequest())
	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
	select {
	case <-firstAttempt:
	case <-time.After(time.Second):
		require.FailNow(t, "first attempt did not complete")
	}
	assert.Equal(t, 1, attempts)
}

// Verifies that payment operations retry transient failure with same operation key.
func TestPaymentOperationsRetryTransientFailureWithSameOperationKey(t *testing.T) {
	tests := []struct {
		name        string
		operation   string
		successBody string
		call        func(*Client) error
	}{
		{name: "capture", operation: "capture", successBody: `{"capture_id":"cap_550e8400-e29b-41d4-a716-446655440001"}`, call: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "void", operation: "void", successBody: `{"void_id":"void_550e8400-e29b-41d4-a716-446655440002"}`, call: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "refund", operation: "refund", successBody: `{"refund_id":"ref_550e8400-e29b-41d4-a716-446655440003"}`, call: func(client *Client) error {
			_, err := client.RefundPayment(context.Background(), validRefundRequest())
			return err
		}},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			var operationKeys []string
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				operationKeys = append(operationKeys, r.Header.Get("Idempotency-Key"))
				if attempts == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.successBody))
			}))
			defer server.Close()

			metrics := &recordingMockBankMetrics{}
			client, err := testClientWithHTTPClient(server.URL, server.Client(), metrics, retryConfig())
			require.NoError(t, err)

			require.NoError(t, tt.call(client))
			assert.Equal(t, []string{"bok_123", "bok_123"}, operationKeys)
			assert.Equal(t, []recordedMockBankRetry{{operation: tt.operation, result: "attempted"}, {operation: tt.operation, result: "succeeded"}}, metrics.retries)
			require.Len(t, metrics.requests, 2)
			assert.Equal(t, []string{"unavailable", "success"}, []string{metrics.requests[0].result, metrics.requests[1].result})
		})
	}
}

// Verifies that payment operations retry exhaustion preserves transient error and metrics.
func TestPaymentOperationsRetryExhaustionPreservesTransientErrorAndMetrics(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		call      func(*Client) error
	}{
		{name: "capture", operation: "capture", call: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "void", operation: "void", call: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "refund", operation: "refund", call: func(client *Client) error {
			_, err := client.RefundPayment(context.Background(), validRefundRequest())
			return err
		}},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			transport := &timeoutThenTimeoutRoundTripper{}
			metrics := &recordingMockBankMetrics{}
			client, err := testClientWithHTTPClient("http://mockbank.example", &http.Client{Transport: transport}, metrics, retryConfig())
			require.NoError(t, err)

			err = tt.call(client)
			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
			assert.Equal(t, 2, transport.calls)
			assert.Equal(t, []recordedMockBankRetry{{operation: tt.operation, result: "attempted"}, {operation: tt.operation, result: "failed"}}, metrics.retries)
			require.Len(t, metrics.requests, 2)
		})
	}
}

// Verifies that payment operations retry timeout then succeed.
func TestPaymentOperationsRetryTimeoutThenSucceed(t *testing.T) {
	tests := []struct {
		name        string
		operation   string
		successBody string
		call        func(*Client) error
	}{
		{name: "capture", operation: "capture", successBody: `{"capture_id":"cap_550e8400-e29b-41d4-a716-446655440001"}`, call: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "void", operation: "void", successBody: `{"void_id":"void_550e8400-e29b-41d4-a716-446655440002"}`, call: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "refund", operation: "refund", successBody: `{"refund_id":"ref_550e8400-e29b-41d4-a716-446655440003"}`, call: func(client *Client) error {
			_, err := client.RefundPayment(context.Background(), validRefundRequest())
			return err
		}},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.successBody))
			}))
			defer server.Close()

			transport := &timeoutThenSuccessRoundTripper{next: http.DefaultTransport}
			metrics := &recordingMockBankMetrics{}
			client, err := testClientWithHTTPClient(server.URL, &http.Client{Transport: transport}, metrics, retryConfig())
			require.NoError(t, err)

			require.NoError(t, tt.call(client))
			assert.Equal(t, 2, transport.calls)
			assert.Equal(t, []recordedMockBankRetry{{operation: tt.operation, result: "attempted"}, {operation: tt.operation, result: "succeeded"}}, metrics.retries)
		})
	}
}

// Verifies that payment operations do not retry definitive outcomes.
func TestPaymentOperationsDoNotRetryDefinitiveOutcomes(t *testing.T) {
	tests := []struct {
		name string
		body string
		kind app.PaymentErrorKind
		call func(*Client) error
	}{
		{name: "capture bank state conflict", body: `{"error":"amount_mismatch","message":"amount mismatch"}`, kind: app.PaymentErrorBankStateConflict, call: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "capture authorization expired", body: `{"error":"authorization_expired","message":"authorization expired"}`, kind: app.PaymentErrorAuthorizationExpired, call: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "capture bank state conflict", body: `{"error":"already_captured","message":"already captured"}`, kind: app.PaymentErrorBankStateConflict, call: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "capture missing bank capture", body: `{"error":"capture_not_found","message":"capture not found"}`, kind: app.PaymentErrorBankStateConflict, call: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "void bank state conflict", body: `{"error":"authorization_not_found","message":"authorization not found"}`, kind: app.PaymentErrorBankStateConflict, call: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "void authorization expired", body: `{"error":"authorization_expired","message":"authorization expired"}`, kind: app.PaymentErrorAuthorizationExpired, call: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "void bank state conflict", body: `{"error":"already_voided","message":"already voided"}`, kind: app.PaymentErrorBankStateConflict, call: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "void missing bank capture", body: `{"error":"capture_not_found","message":"capture not found"}`, kind: app.PaymentErrorBankStateConflict, call: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "refund bank state conflict", body: `{"error":"capture_not_found","message":"capture not found"}`, kind: app.PaymentErrorBankStateConflict, call: func(client *Client) error {
			_, err := client.RefundPayment(context.Background(), validRefundRequest())
			return err
		}},
		{name: "refund bank state conflict", body: `{"error":"already_refunded","message":"already refunded"}`, kind: app.PaymentErrorBankStateConflict, call: func(client *Client) error {
			_, err := client.RefundPayment(context.Background(), validRefundRequest())
			return err
		}},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
			require.NoError(t, err)
			err = tt.call(client)
			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
			assert.Equal(t, 1, attempts)
		})
	}
}

// Verifies that payment operations cancellation stops retry delay.
func TestPaymentOperationsCancellationStopsRetryDelay(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *Client) error
	}{
		{name: "capture", call: func(ctx context.Context, client *Client) error {
			_, err := client.CapturePayment(ctx, validCaptureRequest())
			return err
		}},
		{name: "void", call: func(ctx context.Context, client *Client) error {
			_, err := client.VoidPayment(ctx, validVoidRequest())
			return err
		}},
		{name: "refund", call: func(ctx context.Context, client *Client) error {
			_, err := client.RefundPayment(ctx, validRefundRequest())
			return err
		}},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			firstAttempt := make(chan struct{})
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				close(firstAttempt)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()

			config := retryConfig()
			config.RetryDelay = time.Second
			metrics := &recordingMockBankMetrics{}
			client, err := testClientWithHTTPClient(server.URL, server.Client(), metrics, config)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- tt.call(ctx, client) }()
			select {
			case <-firstAttempt:
				cancel()
			case <-time.After(time.Second):
				require.FailNow(t, "first attempt did not complete")
			}
			select {
			case err := <-result:
				require.Error(t, err)
				assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
			case <-time.After(100 * time.Millisecond):
				require.FailNow(t, "retry delay was not cancelled")
			}
			assert.Equal(t, 1, attempts)
			assert.Empty(t, metrics.retries)
		})
	}
}

// Verifies that mock bank timeout does not extend parent payment command deadline.
func TestMockBankTimeoutDoesNotExtendParentPaymentCommandDeadline(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *Client) error
	}{
		{name: "authorize", call: func(ctx context.Context, client *Client) error {
			_, err := client.AuthorizePayment(ctx, validAuthorizationRequest())
			return err
		}},
		{name: "capture", call: func(ctx context.Context, client *Client) error {
			_, err := client.CapturePayment(ctx, validCaptureRequest())
			return err
		}},
		{name: "void", call: func(ctx context.Context, client *Client) error {
			_, err := client.VoidPayment(ctx, validVoidRequest())
			return err
		}},
		{name: "refund", call: func(ctx context.Context, client *Client) error {
			_, err := client.RefundPayment(ctx, validRefundRequest())
			return err
		}},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			parent, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			parentDeadline, ok := parent.Deadline()
			require.True(t, ok)

			transport := &deadlineRecordingRoundTripper{done: make(chan struct{})}
			config := retryConfig()
			client, err := testClientWithHTTPClient("http://mockbank.example", &http.Client{Transport: transport}, noopMockBankMetrics{}, config)
			require.NoError(t, err)

			err = tt.call(parent, client)

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
			select {
			case <-transport.done:
			case <-time.After(time.Second):
				require.FailNow(t, "mock bank request did not observe the parent deadline")
			}
			require.False(t, transport.deadline.IsZero())
			assert.WithinDuration(t, parentDeadline, transport.deadline, time.Millisecond)
		})
	}
}

// Verifies that mock bank attempt timeout returns bank timeout while payment command remains live.
func TestMockBankAttemptTimeoutReturnsBankTimeoutWhilePaymentCommandRemainsLive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	config := retryConfig()
	config.InitialAttemptTimeout = time.Millisecond
	config.RetryAttemptTimeout = time.Millisecond
	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, config)
	require.NoError(t, err)

	_, err = client.AuthorizePayment(context.Background(), validAuthorizationRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
}

// Verifies that capture payment sends bank payload and operation key.
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

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
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

// Verifies that capture payment returns error for bank failures.
func TestCapturePaymentReturnsErrorForBankFailures(t *testing.T) {
	tests := []struct {
		name string
		code string
		kind app.PaymentErrorKind
	}{
		{name: "amount mismatch", code: "amount_mismatch", kind: app.PaymentErrorBankStateConflict},
		{name: "authorization not found", code: "authorization_not_found", kind: app.PaymentErrorBankStateConflict},
		{name: "authorization already used", code: "authorization_already_used", kind: app.PaymentErrorBankStateConflict},
		{name: "already captured", code: "already_captured", kind: app.PaymentErrorBankStateConflict},
		{name: "internal", code: "internal_error", kind: app.PaymentErrorBankUnavailable},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"` + tt.code + `","message":"capture failed"}`))
			}))
			defer server.Close()

			client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
			require.NoError(t, err)

			_, err = client.CapturePayment(context.Background(), validCaptureRequest())

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
		})
	}
}

// Verifies that capture payment maps expired authorization to lifecycle error.
func TestCapturePaymentMapsExpiredAuthorizationToLifecycleError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_expired","message":"authorization expired"}`))
	}))
	defer server.Close()

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
	require.NoError(t, err)

	_, err = client.CapturePayment(context.Background(), validCaptureRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))
}

// Verifies that void payment sends bank payload and operation key.
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

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
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

// Verifies that void payment returns error for bank failures.
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
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
			require.NoError(t, err)

			_, err = client.VoidPayment(context.Background(), validVoidRequest())

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
		})
	}
}

// Verifies that void payment maps expired authorization to lifecycle error.
func TestVoidPaymentMapsExpiredAuthorizationToLifecycleError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_expired","message":"authorization expired"}`))
	}))
	defer server.Close()

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
	require.NoError(t, err)

	_, err = client.VoidPayment(context.Background(), validVoidRequest())

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))
}

// Verifies that refund payment sends bank payload and operation key.
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

	client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
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

// Verifies that refund payment returns error for bank failures.
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
			kind:   app.PaymentErrorBankStateConflict,
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
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
			require.NoError(t, err)

			_, err = client.RefundPayment(context.Background(), validRefundRequest())

			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, tt.kind))
		})
	}
}

// Verifies that payment operations reject malformed success responses.
func TestPaymentOperationsRejectMalformedSuccessResponses(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		operation func(*Client) error
	}{
		{name: "capture", body: `{"capture_id":""}`, operation: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "void", body: `{"void_id":""}`, operation: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "refund", body: `{"refund_id":""}`, operation: func(client *Client) error {
			_, err := client.RefundPayment(context.Background(), validRefundRequest())
			return err
		}},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := testClientWithHTTPClient(server.URL, server.Client(), noopMockBankMetrics{}, retryConfig())
			require.NoError(t, err)
			assert.True(t, app.HasPaymentErrorKind(tt.operation(client), app.PaymentErrorBankUnavailable))
		})
	}
}

// Verifies that payment operations map bank timeout to timeout error.
func TestPaymentOperationsMapBankTimeoutToTimeoutError(t *testing.T) {
	tests := []struct {
		name      string
		operation func(*Client) error
	}{
		{name: "capture", operation: func(client *Client) error {
			_, err := client.CapturePayment(context.Background(), validCaptureRequest())
			return err
		}},
		{name: "void", operation: func(client *Client) error {
			_, err := client.VoidPayment(context.Background(), validVoidRequest())
			return err
		}},
		{name: "refund", operation: func(client *Client) error {
			_, err := client.RefundPayment(context.Background(), validRefundRequest())
			return err
		}},
	}

	for _, tt := range tests {
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(50 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			httpClient := server.Client()
			httpClient.Timeout = time.Nanosecond
			client, err := testClientWithHTTPClient(server.URL, httpClient, noopMockBankMetrics{}, retryConfig())
			require.NoError(t, err)
			err = tt.operation(client)
			require.Error(t, err)
			assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout))
		})
	}
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

type timeoutThenTimeoutRoundTripper struct{ calls int }

func (t *timeoutThenTimeoutRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, timeoutError{}
}

type timeoutThenSuccessRoundTripper struct {
	calls int
	next  http.RoundTripper
}

func (t *timeoutThenSuccessRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	t.calls++
	if t.calls == 1 {
		return nil, timeoutError{}
	}
	return t.next.RoundTrip(request)
}

type deadlineRecordingRoundTripper struct {
	deadline time.Time
	done     chan struct{}
	doneOnce sync.Once
}

func (t *deadlineRecordingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	t.deadline, _ = request.Context().Deadline()
	t.doneOnce.Do(func() { close(t.done) })
	return nil, timeoutError{}
}

type recordingMockBankMetrics struct {
	requests []recordedMockBankRequest
	retries  []recordedMockBankRetry
}

type noopMockBankMetrics struct{}

func (noopMockBankMetrics) RecordMockBankRetry(string, string) {}

func (noopMockBankMetrics) RecordMockBankRequest(string, string, time.Duration) {}

func (m *recordingMockBankMetrics) RecordMockBankRetry(operation string, result string) {
	m.retries = append(m.retries, recordedMockBankRetry{operation: operation, result: result})
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

type recordedMockBankRetry struct {
	operation string
	result    string
}

// testClientWithHTTPClient keeps transport control local to adapter tests while
// exercising the production constructor for configuration and URL validation.
func testClientWithHTTPClient(baseURL string, httpClient *http.Client, metrics metrics, config Config) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	config.BaseURL = *parsed
	client, err := NewClient(metrics, config)
	if err != nil {
		return nil, err
	}
	if httpClient != nil {
		client.httpClient = httpClient
	}
	return client, nil
}

func retryConfig() Config {
	return Config{
		InitialAttemptTimeout: time.Second,
		RetryDelay:            time.Millisecond,
		RetryAttemptTimeout:   time.Second,
		ConnectTimeout:        time.Second,
		TLSHandshakeTimeout:   time.Second,
		ResponseHeaderTimeout: time.Second,
		IdleConnectionTimeout: time.Second,
	}
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

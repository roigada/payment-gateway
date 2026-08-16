package mockbank

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientBuildsConfiguredHTTPTransport(t *testing.T) {
	config := retryConfig()
	config.BaseURL = "https://mockbank.example"
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

func TestNewHTTPRequestBuildsMockBankRequest(t *testing.T) {
	config := retryConfig()
	config.BaseURL = "https://mockbank.example"
	client, err := NewClient(noopMockBankMetrics{}, config)
	require.NoError(t, err)

	request, err := client.newHTTPRequest(context.Background(), "/api/v1/authorizations", &bytes.Buffer{}, "bok_123")

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, request.Method)
	assert.Equal(t, "https://mockbank.example/api/v1/authorizations", request.URL.String())
	assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
	assert.Equal(t, "bok_123", request.Header.Get("Idempotency-Key"))
}

func TestNewClientRejectsNilMetrics(t *testing.T) {
	client, err := NewClient(nil, Config{BaseURL: "https://mockbank.example"})

	require.Nil(t, client)
	require.EqualError(t, err, "mock bank metrics are required")
}

func TestNewClientRejectsInvalidConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"base URL", func(config *Config) { config.BaseURL = "relative" }, "mock bank base URL must be absolute"},
		{"timeout", func(config *Config) { config.Timeout = 0 }, "mock bank timeout must be positive"},
		{"initial attempt timeout", func(config *Config) { config.InitialAttemptTimeout = 0 }, "mock bank initial attempt timeout must be positive"},
		{"retry delay", func(config *Config) { config.RetryDelay = 0 }, "mock bank retry delay must be positive"},
		{"retry attempt timeout", func(config *Config) { config.RetryAttemptTimeout = 0 }, "mock bank retry attempt timeout must be positive"},
		{"connect timeout", func(config *Config) { config.ConnectTimeout = 0 }, "mock bank connect timeout must be positive"},
		{"TLS handshake timeout", func(config *Config) { config.TLSHandshakeTimeout = 0 }, "mock bank TLS handshake timeout must be positive"},
		{"response header timeout", func(config *Config) { config.ResponseHeaderTimeout = 0 }, "mock bank response header timeout must be positive"},
		{"idle connection timeout", func(config *Config) { config.IdleConnectionTimeout = 0 }, "mock bank idle connection timeout must be positive"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := retryConfig()
			config.BaseURL = "https://mockbank.example"
			tt.mutate(&config)

			client, err := NewClient(noopMockBankMetrics{}, config)

			require.Nil(t, client)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestDecodeBadRequestAuthorizationOutcomeReturnsGatewayReason(t *testing.T) {
	reason, declineReason, err := decodeBadRequestAuthorizationOutcome(&http.Response{
		Body: io.NopCloser(strings.NewReader(`{"error":"invalid_card"}`)),
	})

	require.NoError(t, err)
	assert.Equal(t, "card details are invalid", reason)
	assert.Empty(t, declineReason)
}

func TestDecodeBadRequestAuthorizationOutcomeReturnsExpiredCardDecline(t *testing.T) {
	reason, declineReason, err := decodeBadRequestAuthorizationOutcome(&http.Response{
		Body: io.NopCloser(strings.NewReader(`{"error":"card_expired"}`)),
	})

	require.NoError(t, err)
	assert.Empty(t, reason)
	assert.Equal(t, domain.DeclineReasonExpiredCard, declineReason)
}

func TestDecodeBadRequestAuthorizationOutcomeReturnsRawDecodeFailure(t *testing.T) {
	_, _, err := decodeBadRequestAuthorizationOutcome(&http.Response{
		Body: io.NopCloser(strings.NewReader(`{`)),
	})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "bank is unavailable")
}

package mockbank

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientBuildsConfiguredHTTPTransport(t *testing.T) {
	client, err := NewClient(nil, ClientConfig{
		BaseURL:               "https://mockbank.example",
		TLSHandshakeTimeout:   2,
		ResponseHeaderTimeout: 3,
		IdleConnectionTimeout: 4,
	})

	require.NoError(t, err)
	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, time.Duration(2), transport.TLSHandshakeTimeout)
	assert.Equal(t, time.Duration(3), transport.ResponseHeaderTimeout)
	assert.Equal(t, time.Duration(4), transport.IdleConnTimeout)
}

func TestNewHTTPRequestBuildsMockBankRequest(t *testing.T) {
	client, err := NewClient(nil, ClientConfig{BaseURL: "https://mockbank.example"})
	require.NoError(t, err)

	request, err := client.newHTTPRequest(context.Background(), "/api/v1/authorizations", &bytes.Buffer{}, "bok_123")

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, request.Method)
	assert.Equal(t, "https://mockbank.example/api/v1/authorizations", request.URL.String())
	assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
	assert.Equal(t, "bok_123", request.Header.Get("Idempotency-Key"))
}

func TestDecodeBadRequestInvalidInputReasonReturnsGatewayReason(t *testing.T) {
	reason, err := decodeBadRequestInvalidInputReason(&http.Response{
		Body: io.NopCloser(strings.NewReader(`{"error":"invalid_card"}`)),
	})

	require.NoError(t, err)
	assert.Equal(t, "card details are invalid", reason)
}

func TestDecodeBadRequestInvalidInputReasonReturnsRawDecodeFailure(t *testing.T) {
	_, err := decodeBadRequestInvalidInputReason(&http.Response{
		Body: io.NopCloser(strings.NewReader(`{`)),
	})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "bank is unavailable")
}

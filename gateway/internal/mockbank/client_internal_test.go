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

func TestNewAuthorizationHTTPRequestReturnsBuildFailure(t *testing.T) {
	_, err := newAuthorizationHTTPRequest(context.Background(), "\n", &bytes.Buffer{}, "bok_123")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "internal server error")
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

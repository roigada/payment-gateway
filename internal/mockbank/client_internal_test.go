package mockbank

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

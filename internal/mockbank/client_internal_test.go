package mockbank

import (
	"bytes"
	"context"
	"testing"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuthorizationHTTPRequestMapsLocalBuildFailuresToInternalError(t *testing.T) {
	_, err := newAuthorizationHTTPRequest(context.Background(), "\n", &bytes.Buffer{}, "bok_123")

	require.Error(t, err)
	assert.True(t, app.IsPaymentErrorKind(err, app.PaymentErrorInternal))
}

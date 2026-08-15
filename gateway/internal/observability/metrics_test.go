package observability

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewMetricsRegistersGatewayMetricsAndCollectors(t *testing.T) {
	metrics, err := NewMetrics(&sql.DB{}, pendingPaymentMetricsSource{count: 2})
	require.NoError(t, err)

	require.NotNil(t, metrics.Handler)
	require.NotNil(t, metrics.HTTP)
	require.NotNil(t, metrics.RateLimit)
	require.NotNil(t, metrics.MockBank)
	require.NotNil(t, metrics.PaymentOperations)
	require.NotNil(t, metrics.IdempotencyReplayCleanup)

	recorder := httptest.NewRecorder()
	metrics.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
}

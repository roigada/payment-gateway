package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Verifies that new handler serves only metrics.
func TestNewHandlerServesOnlyMetrics(t *testing.T) {
	handler := NewHandler(NewRegistry())

	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusOK, metrics.Code)

	notMetrics := httptest.NewRecorder()
	handler.ServeHTTP(notMetrics, httptest.NewRequest(http.MethodGet, "/not-metrics", nil))
	assert.Equal(t, http.StatusNotFound, notMetrics.Code)
}

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMaxJSONBodyBytes int64 = 64 * 1024

// JSON decoding distinguishes empty, malformed, type-mismatched, unknown-field, oversized, and
// multi-value payloads while preserving the common invalid-body classification for HTTP handlers.
func TestDecodeJSONRequestReturnsInvalidBodyCategoryErrors(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		category error
		contains string
	}{
		{
			name:     "empty body",
			body:     "",
			category: errEmptyJSONBody,
		},
		{
			name:     "malformed body",
			body:     `{"order_id":`,
			category: errMalformedJSONBody,
		},
		{
			name:     "incorrect type",
			body:     `{"order_id":1}`,
			category: errIncorrectJSONType,
		},
		{
			name:     "unknown field",
			body:     `{"order_id":"order-1","unexpected":true}`,
			category: errUnknownJSONField,
			contains: `"unexpected"`,
		},
		{
			name:     "oversized body",
			body:     `{"order_id":"` + strings.Repeat("a", int(testMaxJSONBodyBytes)) + `"}`,
			category: errOversizedJSONBody,
		},
		{
			name:     "multiple values",
			body:     `{"order_id":"order-1"} {"order_id":"order-2"}`,
			category: errMultipleJSONValues,
		},
		{
			name:     "trailing junk",
			body:     `{"order_id":"order-1"} garbage`,
			category: errMalformedJSONBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := decodePaymentRequest(tt.body)

			require.Error(t, err)
			assert.ErrorIs(t, err, errInvalidJSONBody)
			assert.ErrorIs(t, err, tt.category)
			if tt.contains != "" {
				assert.Contains(t, err.Error(), tt.contains)
			}
		})
	}
}

// A programming error such as a nil decode destination remains visible as a panic rather than
// being misreported to an API client as malformed input.
func TestDecodeJSONRequestPanicsForInvalidDestination(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(`{"order_id":"order-1"}`))

	assert.Panics(t, func() {
		_ = decodeJSONRequest(rec, req, nil, testMaxJSONBodyBytes)
	})
}

// Each caller's supplied size limit governs decoding, even when it is smaller than the default test limit.
func TestDecodeJSONRequestUsesProvidedBodyLimit(t *testing.T) {
	body := `{"order_id":"order-1"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(body))
	var request struct {
		OrderID string `json:"order_id"`
	}

	err := decodeJSONRequest(rec, req, &request, int64(len(body)-1))

	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidJSONBody)
	assert.ErrorIs(t, err, errOversizedJSONBody)
}

// Bodyless endpoints accept both server-created empty bodies and an explicitly absent request body.
func TestRequireEmptyRequestBodyAcceptsAbsentOrEmptyBody(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "nil body",
			req: &http.Request{
				Body: nil,
			},
		},
		{
			name: "server request with empty body reader",
			req:  httptest.NewRequest(http.MethodPost, "/api/v1/payments/pay_1/capture", nil),
		},
		{
			name: "explicit empty body reader",
			req:  httptest.NewRequest(http.MethodPost, "/api/v1/payments/pay_1/capture", strings.NewReader("")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, requireEmptyRequestBody(tt.req))
		})
	}
}

// Bodyless endpoints reject every byte, including JSON and insignificant-looking whitespace.
func TestRequireEmptyRequestBodyRejectsAnyBodyBytes(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "json object", body: "{}"},
		{name: "whitespace", body: " \n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/pay_1/capture", strings.NewReader(tt.body))

			err := requireEmptyRequestBody(req)

			require.Error(t, err)
			assert.ErrorIs(t, err, errNonEmptyBody)
		})
	}
}

func decodePaymentRequest(body string) error {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(body))

	var request struct {
		OrderID string `json:"order_id"`
	}
	return decodeJSONRequest(rec, req, &request, testMaxJSONBodyBytes)
}

// Successful JSON responses set the requested status and JSON media type, and use the encoder's
// newline-terminated representation.
func TestWriteJSONWritesStatusContentTypeAndBody(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusCreated, map[string]string{"id": "pay_550e8400-e29b-41d4-a716-446655440000"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id":"pay_550e8400-e29b-41d4-a716-446655440000"}`, rec.Body.String())
	assert.Equal(t, "{\"id\":\"pay_550e8400-e29b-41d4-a716-446655440000\"}\n", rec.Body.String())
}

// An encoding failure before the response starts falls back to Go's standard 500 response instead
// of emitting a partial JSON success response.
func TestWriteJSONReturnsInternalServerErrorWhenBodyCannotBeEncoded(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusCreated, map[string]any{"bad": make(chan int)})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "Internal Server Error\n", rec.Body.String())
}

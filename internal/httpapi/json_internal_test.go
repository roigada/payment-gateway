package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			body:     `{"order_id":"` + strings.Repeat("a", maxJSONBodyBytes) + `"}`,
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

func TestDecodeJSONRequestPanicsForInvalidDestination(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(`{"order_id":"order-1"}`))

	assert.Panics(t, func() {
		_ = decodeJSONRequest(rec, req, nil)
	})
}

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
			req:  httptest.NewRequest(http.MethodPost, "/v1/payments/pay_1/capture", nil),
		},
		{
			name: "explicit empty body reader",
			req:  httptest.NewRequest(http.MethodPost, "/v1/payments/pay_1/capture", strings.NewReader("")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, requireEmptyRequestBody(tt.req))
		})
	}
}

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
			req := httptest.NewRequest(http.MethodPost, "/v1/payments/pay_1/capture", strings.NewReader(tt.body))

			err := requireEmptyRequestBody(req)

			require.Error(t, err)
			assert.ErrorIs(t, err, errNonEmptyBody)
		})
	}
}

func decodePaymentRequest(body string) error {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(body))

	var request struct {
		OrderID string `json:"order_id"`
	}
	return decodeJSONRequest(rec, req, &request)
}

func TestWriteJSONWritesStatusContentTypeAndBody(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusCreated, map[string]string{"id": "pay_550e8400-e29b-41d4-a716-446655440000"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id":"pay_550e8400-e29b-41d4-a716-446655440000"}`, rec.Body.String())
	assert.Equal(t, "{\"id\":\"pay_550e8400-e29b-41d4-a716-446655440000\"}\n", rec.Body.String())
}

func TestWriteJSONReturnsInternalServerErrorWhenBodyCannotBeEncoded(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusCreated, map[string]any{"bad": make(chan int)})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "Internal Server Error\n", rec.Body.String())
}

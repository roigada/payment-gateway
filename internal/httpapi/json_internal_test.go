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
			body:     `{"title":`,
			category: errMalformedJSONBody,
		},
		{
			name:     "incorrect type",
			body:     `{"title":1}`,
			category: errIncorrectJSONType,
		},
		{
			name:     "unknown field",
			body:     `{"title":"Buy milk","completed":true}`,
			category: errUnknownJSONField,
			contains: `"completed"`,
		},
		{
			name:     "oversized body",
			body:     `{"title":"` + strings.Repeat("a", maxJSONBodyBytes) + `"}`,
			category: errOversizedJSONBody,
		},
		{
			name:     "multiple values",
			body:     `{"title":"Buy milk"} {"title":"Pay rent"}`,
			category: errMultipleJSONValues,
		},
		{
			name:     "trailing junk",
			body:     `{"title":"Buy milk"} garbage`,
			category: errMalformedJSONBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := decodeTaskRequest(tt.body)

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
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"title":"Buy milk"}`))

	assert.Panics(t, func() {
		_ = decodeJSONRequest(rec, req, nil)
	})
}

func decodeTaskRequest(body string) error {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))

	var request struct {
		Title string `json:"title"`
	}
	return decodeJSONRequest(rec, req, &request)
}

func TestWriteJSONWritesStatusContentTypeAndBody(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusCreated, map[string]string{"id": "task-1"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id":"task-1"}`, rec.Body.String())
	assert.Equal(t, "{\"id\":\"task-1\"}\n", rec.Body.String())
}

func TestWriteJSONReturnsInternalServerErrorWhenBodyCannotBeEncoded(t *testing.T) {
	rec := httptest.NewRecorder()

	writeJSON(rec, http.StatusCreated, map[string]any{"bad": make(chan int)})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "Internal Server Error\n", rec.Body.String())
}

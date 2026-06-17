package exampleapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/exampleapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifyTaskCreatedSendsRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotPayload))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client, err := exampleapi.NewClient(server.URL, "secret-token", nil)
	require.NoError(t, err)

	err = client.NotifyTaskCreated(context.Background(), app.TaskResult{
		ID:        "task-1",
		Title:     "Buy milk",
		Completed: false,
	})
	require.NoError(t, err)

	assert.Equal(t, "/task-created", gotPath)
	assert.Equal(t, "Bearer secret-token", gotAuth)
	assert.Equal(t, map[string]any{
		"task_id":   "task-1",
		"title":     "Buy milk",
		"completed": false,
	}, gotPayload)
}

func TestNotifyTaskCompletedSendsRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotPayload))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client, err := exampleapi.NewClient(server.URL, "secret-token", nil)
	require.NoError(t, err)

	err = client.NotifyTaskCompleted(context.Background(), app.TaskResult{
		ID:        "task-1",
		Title:     "Buy milk",
		Completed: true,
	})
	require.NoError(t, err)

	assert.Equal(t, "/task-completed", gotPath)
	assert.Equal(t, "Bearer secret-token", gotAuth)
	assert.Equal(t, map[string]any{
		"task_id":   "task-1",
		"title":     "Buy milk",
		"completed": true,
	}, gotPayload)
}

func TestNotifyTaskCreatedReturnsErrorForNonSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	client, err := exampleapi.NewClient(server.URL, "secret-token", nil)
	require.NoError(t, err)

	err = client.NotifyTaskCreated(context.Background(), app.TaskResult{
		ID:        "task-1",
		Title:     "Buy milk",
		Completed: false,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 401")
}

func TestNewClientReturnsErrorForInvalidBaseURL(t *testing.T) {
	_, err := exampleapi.NewClient("not-a-url", "secret-token", nil)
	require.Error(t, err)
}

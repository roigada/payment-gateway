package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roigada/template-go/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTaskNotifierUsesNoopWhenExampleAPIBaseURLIsAbsent(t *testing.T) {
	notifier, err := newTaskNotifier("", "")
	require.NoError(t, err)

	err = notifier.NotifyTaskCreated(context.Background(), app.TaskResult{
		ID:    "task-1",
		Title: "Buy milk",
	})
	require.NoError(t, err)
}

func TestNewTaskNotifierRequiresTokenWhenExampleAPIBaseURLIsSet(t *testing.T) {
	_, err := newTaskNotifier("https://example.test", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "EXAMPLE_API_TOKEN is required")
}

func TestNewTaskNotifierUsesExampleAPIWhenConfigured(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
		assert.Equal(t, "/task-created", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	notifier, err := newTaskNotifier(server.URL, "secret-token")
	require.NoError(t, err)

	err = notifier.NotifyTaskCreated(context.Background(), app.TaskResult{
		ID:    "task-1",
		Title: "Buy milk",
	})
	require.NoError(t, err)
	assert.True(t, called)
}

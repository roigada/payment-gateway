package domain_test

import (
	"testing"

	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTaskCreatesIncompleteTaskWithTrimmedTitle(t *testing.T) {
	task, err := domain.NewTask(domain.TaskID("task-1"), "  Buy milk  ")
	require.NoError(t, err)

	assert.Equal(t, domain.TaskID("task-1"), task.ID())
	assert.Equal(t, "Buy milk", task.Title())
	assert.False(t, task.Completed())
}

func TestNewTaskRejectsEmptyTitle(t *testing.T) {
	_, err := domain.NewTask(domain.TaskID("task-1"), " \n\t ")
	assert.ErrorIs(t, err, domain.ErrInvalidTaskTitle)
}

func TestTaskCanBeCompletedAndReopened(t *testing.T) {
	task, err := domain.NewTask(domain.TaskID("task-1"), "Buy milk")
	require.NoError(t, err)

	task.Complete()
	assert.True(t, task.Completed())

	task.Reopen()
	assert.False(t, task.Completed())
}

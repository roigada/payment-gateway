package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/roigada/template-go/internal/app"
	"github.com/roigada/template-go/internal/domain"
	"github.com/roigada/template-go/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateTaskGeneratesTaskIDAndCreatesTask(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	service := newTaskService(repo)

	task, err := service.CreateTask(context.Background(), " Buy milk ")
	require.NoError(t, err)

	assert.Equal(t, "task-1", task.ID)

	saved, err := repo.FindByID(context.Background(), domain.TaskID("task-1"))
	require.NoError(t, err)

	assert.Equal(t, "Buy milk", saved.Title())
}

func TestCreateTaskNotifiesTaskCreated(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	notifier := &recordingTaskNotifier{}
	service := app.NewTaskService(repo, testsupport.FixedTaskIDGenerator{ID: domain.TaskID("task-1")}, notifier)

	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)

	require.Len(t, notifier.created, 1)
	assert.Equal(t, app.TaskResult{
		ID:        "task-1",
		Title:     "Buy milk",
		Completed: false,
	}, notifier.created[0])
}

func TestCreateTaskReturnsNotifierErrorAfterSavingTask(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	notifierErr := errors.New("notify failed")
	service := app.NewTaskService(
		repo,
		testsupport.FixedTaskIDGenerator{ID: domain.TaskID("task-1")},
		&recordingTaskNotifier{createdErr: notifierErr},
	)

	_, err := service.CreateTask(context.Background(), "Buy milk")
	assert.ErrorIs(t, err, notifierErr)

	saved, err := repo.FindByID(context.Background(), domain.TaskID("task-1"))
	require.NoError(t, err)
	assert.Equal(t, "Buy milk", saved.Title())
}

func TestCreateTaskReturnsErrorWhenGeneratedTaskIDExists(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	service := newTaskService(repo)
	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)

	_, err = service.CreateTask(context.Background(), "Pay rent")
	require.Error(t, err)

	saved, err := repo.FindByID(context.Background(), domain.TaskID("task-1"))
	require.NoError(t, err)
	assert.Equal(t, "Buy milk", saved.Title())
}

func TestListTasksReturnsSavedTasks(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	service := newTaskService(repo)

	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)

	tasks, err := service.ListTasks(context.Background())
	require.NoError(t, err)

	require.Len(t, tasks, 1)
	assert.Equal(t, "Buy milk", tasks[0].Title)
}

func TestGetTaskReturnsTaskByID(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	service := newTaskService(repo)

	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)

	task, err := service.GetTask(context.Background(), "task-1")
	require.NoError(t, err)

	assert.Equal(t, "Buy milk", task.Title)
}

func TestGetTaskReturnsNotFound(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	service := newTaskService(repo)

	_, err := service.GetTask(context.Background(), "missing")
	assert.ErrorIs(t, err, app.ErrTaskNotFound)
}

func TestCompleteTaskCompletesTask(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	service := newTaskService(repo)
	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)

	task, err := service.CompleteTask(context.Background(), "task-1")
	require.NoError(t, err)

	assert.True(t, task.Completed)

	saved, err := repo.FindByID(context.Background(), domain.TaskID("task-1"))
	require.NoError(t, err)
	assert.True(t, saved.Completed())
}

func TestCompleteTaskNotifiesTaskCompletedEveryTime(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	notifier := &recordingTaskNotifier{}
	service := app.NewTaskService(repo, testsupport.FixedTaskIDGenerator{ID: domain.TaskID("task-1")}, notifier)
	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)

	_, err = service.CompleteTask(context.Background(), "task-1")
	require.NoError(t, err)
	_, err = service.CompleteTask(context.Background(), "task-1")
	require.NoError(t, err)

	require.Len(t, notifier.completed, 2)
	assert.Equal(t, app.TaskResult{
		ID:        "task-1",
		Title:     "Buy milk",
		Completed: true,
	}, notifier.completed[0])
	assert.Equal(t, notifier.completed[0], notifier.completed[1])
}

func TestCompleteTaskReturnsNotifierErrorAfterSavingTask(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	notifierErr := errors.New("notify failed")
	service := app.NewTaskService(
		repo,
		testsupport.FixedTaskIDGenerator{ID: domain.TaskID("task-1")},
		&recordingTaskNotifier{completedErr: notifierErr},
	)
	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)

	_, err = service.CompleteTask(context.Background(), "task-1")
	assert.ErrorIs(t, err, notifierErr)

	saved, err := repo.FindByID(context.Background(), domain.TaskID("task-1"))
	require.NoError(t, err)
	assert.True(t, saved.Completed())
}

func TestReopenTaskReopensTask(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	service := newTaskService(repo)
	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)
	task, err := repo.FindByID(context.Background(), domain.TaskID("task-1"))
	require.NoError(t, err)
	task.Complete()
	require.NoError(t, repo.Update(context.Background(), task))

	reopened, err := service.ReopenTask(context.Background(), "task-1")
	require.NoError(t, err)

	assert.False(t, reopened.Completed)

	saved, err := repo.FindByID(context.Background(), domain.TaskID("task-1"))
	require.NoError(t, err)
	assert.False(t, saved.Completed())
}

func TestCompleteTaskReturnsNotFound(t *testing.T) {
	service := newTaskService(testsupport.NewTaskRepository())

	_, err := service.CompleteTask(context.Background(), "missing")
	assert.ErrorIs(t, err, app.ErrTaskNotFound)
}

func TestDeleteTaskRemovesTask(t *testing.T) {
	repo := testsupport.NewTaskRepository()
	service := newTaskService(repo)
	_, err := service.CreateTask(context.Background(), "Buy milk")
	require.NoError(t, err)

	require.NoError(t, service.DeleteTask(context.Background(), "task-1"))

	_, err = service.GetTask(context.Background(), "task-1")
	assert.ErrorIs(t, err, app.ErrTaskNotFound)
}

func TestDeleteTaskReturnsNotFound(t *testing.T) {
	service := newTaskService(testsupport.NewTaskRepository())

	err := service.DeleteTask(context.Background(), "missing")
	assert.ErrorIs(t, err, app.ErrTaskNotFound)
}

func newTaskService(repo app.TaskRepository) *app.TaskService {
	return app.NewTaskService(
		repo,
		testsupport.FixedTaskIDGenerator{ID: domain.TaskID("task-1")},
		app.NoopTaskNotifier{},
	)
}

type recordingTaskNotifier struct {
	created      []app.TaskResult
	completed    []app.TaskResult
	createdErr   error
	completedErr error
}

func (n *recordingTaskNotifier) NotifyTaskCreated(_ context.Context, task app.TaskResult) error {
	n.created = append(n.created, task)
	return n.createdErr
}

func (n *recordingTaskNotifier) NotifyTaskCompleted(_ context.Context, task app.TaskResult) error {
	n.completed = append(n.completed, task)
	return n.completedErr
}

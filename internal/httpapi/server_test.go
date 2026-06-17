package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostTasksCreatesTask(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.createTaskResult = newTask(t, "task-1", "Buy milk", false)
	rec := api.request(t, http.MethodPost, "/v1/tasks", `{"title":" Buy milk "}`)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "/v1/tasks/task-1", rec.Header().Get("Location"))
	assert.Equal(t, " Buy milk ", api.tasks.createTaskTitle)

	assert.JSONEq(t, `{"task":{"id":"task-1","title":"Buy milk","completed":false}}`, rec.Body.String())
}

func TestHealthzReturnsNoContent(t *testing.T) {
	api := newTaskAPITest(t)
	rec := api.request(t, http.MethodGet, "/healthz", "")

	assert.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Body.String())
	assert.False(t, api.readiness.called)
}

func TestReadyzReturnsNoContentWhenDatabaseIsAvailable(t *testing.T) {
	api := newTaskAPITest(t)
	rec := api.request(t, http.MethodGet, "/readyz", "")

	assert.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Body.String())
	assert.True(t, api.readiness.called)
}

func TestReadyzReturnsUnavailableWhenDatabaseIsUnavailable(t *testing.T) {
	api := newTaskAPITest(t)
	api.readiness.err = errors.New("ping failed")
	rec := api.request(t, http.MethodGet, "/readyz", "")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "database_unavailable", "database unavailable")
}

func TestUnversionedTaskRoutesAreNotRegistered(t *testing.T) {
	api := newTaskAPITest(t)
	rec := api.request(t, http.MethodGet, "/tasks", "")

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestPostTasksRejectsEmptyTitle(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.createTaskErr = domain.ErrInvalidTaskTitle
	rec := api.request(t, http.MethodPost, "/v1/tasks", `{"title":"   "}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())

	assert.JSONEq(t, `{"error":{"code":"invalid_task_title","message":"invalid task title"}}`, rec.Body.String())
}

func TestPostTasksRejectsMalformedJSON(t *testing.T) {
	api := newTaskAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/tasks", `{"title":`)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	assert.JSONEq(t, `{"error":{"code":"invalid_json_body","message":"invalid JSON body"}}`, rec.Body.String())
}

func TestPostTasksRejectsUnknownFields(t *testing.T) {
	api := newTaskAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/tasks", `{"title":"Buy milk","completed":true}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	assertErrorResponse(t, rec, "invalid_json_body", "invalid JSON body")
}

func TestPostTasksRejectsTrailingContent(t *testing.T) {
	api := newTaskAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/tasks", `{"title":"Buy milk"} {"title":"Pay rent"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	assertErrorResponse(t, rec, "invalid_json_body", "invalid JSON body")
}

func TestPostTasksRejectsOversizedBody(t *testing.T) {
	api := newTaskAPITest(t)
	rec := api.request(t, http.MethodPost, "/v1/tasks", `{"title":"`+strings.Repeat("a", 1<<20)+`"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())

	assertErrorResponse(t, rec, "invalid_json_body", "invalid JSON body")
}

func TestGetTasksListsTasks(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.listTasksResult = []app.TaskResult{
		newTask(t, "task-1", "Buy milk", false),
	}
	rec := api.request(t, http.MethodGet, "/v1/tasks", "")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	assert.JSONEq(t, `{"tasks":[{"id":"task-1","title":"Buy milk","completed":false}]}`, rec.Body.String())
}

func TestGetTasksRecoversPanic(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.listTasksPanic = "database pool exploded"
	rec := api.request(t, http.MethodGet, "/v1/tasks", "")

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "close", rec.Header().Get("Connection"))
	assertErrorResponse(t, rec, "internal_server_error", "Internal Server Error")
}

func TestGetTaskReturnsTaskByID(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.getTaskResult = newTask(t, "task-1", "Buy milk", false)
	rec := api.request(t, http.MethodGet, "/v1/tasks/task-1", "")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "task-1", api.tasks.getTaskID)

	assert.JSONEq(t, `{"task":{"id":"task-1","title":"Buy milk","completed":false}}`, rec.Body.String())
}

func TestGetTaskReturnsNotFound(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.getTaskErr = app.ErrTaskNotFound
	rec := api.request(t, http.MethodGet, "/v1/tasks/missing", "")

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "task_not_found", "task not found")
}

func TestPostTaskCompleteCompletesTask(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.completeTaskResult = newTask(t, "task-1", "Buy milk", true)
	rec := api.request(t, http.MethodPost, "/v1/tasks/task-1/complete", "")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "task-1", api.tasks.completeTaskID)

	assert.JSONEq(t, `{"task":{"id":"task-1","title":"Buy milk","completed":true}}`, rec.Body.String())
}

func TestPostTaskReopenReopensTask(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.reopenTaskResult = newTask(t, "task-1", "Buy milk", false)
	rec := api.request(t, http.MethodPost, "/v1/tasks/task-1/reopen", "")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "task-1", api.tasks.reopenTaskID)

	assert.JSONEq(t, `{"task":{"id":"task-1","title":"Buy milk","completed":false}}`, rec.Body.String())
}

func TestPostTaskCompleteReturnsNotFound(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.completeTaskErr = app.ErrTaskNotFound
	rec := api.request(t, http.MethodPost, "/v1/tasks/missing/complete", "")

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "task_not_found", "task not found")
}

func TestDeleteTaskRemovesTask(t *testing.T) {
	api := newTaskAPITest(t)
	rec := api.request(t, http.MethodDelete, "/v1/tasks/task-1", "")

	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "task-1", api.tasks.deleteTaskID)
}

func TestDeleteTaskReturnsNotFound(t *testing.T) {
	api := newTaskAPITest(t)
	api.tasks.deleteTaskErr = app.ErrTaskNotFound
	rec := api.request(t, http.MethodDelete, "/v1/tasks/missing", "")

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assertErrorResponse(t, rec, "task_not_found", "task not found")
}

type taskAPITest struct {
	tasks     *taskUseCasesFake
	readiness *readinessCheckerFake
	handler   http.Handler
}

func newTaskAPITest(t *testing.T) *taskAPITest {
	t.Helper()

	tasks := &taskUseCasesFake{}
	readiness := &readinessCheckerFake{}

	return &taskAPITest{
		tasks:     tasks,
		readiness: readiness,
		handler:   httpapi.NewServerWithReadiness(tasks, discardLogger(), readiness),
	}
}

func (api *taskAPITest) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()

	api.handler.ServeHTTP(rec, req)

	return rec
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var body T
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return body
}

func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, code, message string) {
	t.Helper()

	body := decodeJSON[errorResponse](t, rec)
	assert.Equal(t, code, body.Error.Code)
	assert.Equal(t, message, body.Error.Message)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type taskUseCasesFake struct {
	createTaskTitle  string
	createTaskResult app.TaskResult
	createTaskErr    error

	listTasksResult []app.TaskResult
	listTasksErr    error
	listTasksPanic  any

	getTaskID     string
	getTaskResult app.TaskResult
	getTaskErr    error

	completeTaskID     string
	completeTaskResult app.TaskResult
	completeTaskErr    error

	reopenTaskID     string
	reopenTaskResult app.TaskResult
	reopenTaskErr    error

	deleteTaskID  string
	deleteTaskErr error
}

type readinessCheckerFake struct {
	called bool
	err    error
}

func (f *readinessCheckerFake) PingContext(context.Context) error {
	f.called = true
	return f.err
}

func (f *taskUseCasesFake) CreateTask(_ context.Context, title string) (app.TaskResult, error) {
	f.createTaskTitle = title
	return f.createTaskResult, f.createTaskErr
}

func (f *taskUseCasesFake) ListTasks(_ context.Context) ([]app.TaskResult, error) {
	if f.listTasksPanic != nil {
		panic(f.listTasksPanic)
	}
	return f.listTasksResult, f.listTasksErr
}

func (f *taskUseCasesFake) GetTask(_ context.Context, id string) (app.TaskResult, error) {
	f.getTaskID = id
	return f.getTaskResult, f.getTaskErr
}

func (f *taskUseCasesFake) CompleteTask(_ context.Context, id string) (app.TaskResult, error) {
	f.completeTaskID = id
	return f.completeTaskResult, f.completeTaskErr
}

func (f *taskUseCasesFake) ReopenTask(_ context.Context, id string) (app.TaskResult, error) {
	f.reopenTaskID = id
	return f.reopenTaskResult, f.reopenTaskErr
}

func (f *taskUseCasesFake) DeleteTask(_ context.Context, id string) error {
	f.deleteTaskID = id
	return f.deleteTaskErr
}

func newTask(t *testing.T, id, title string, completed bool) app.TaskResult {
	t.Helper()

	return app.TaskResult{
		ID:        id,
		Title:     title,
		Completed: completed,
	}
}

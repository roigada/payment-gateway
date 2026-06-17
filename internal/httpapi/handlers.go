package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/roigada/payment-gateway/internal/app"
)

const (
	errorCodeInternalServer   = "internal_server_error"
	errorCodeInvalidJSONBody  = "invalid_json_body"
	errorCodeInvalidTaskTitle = "invalid_task_title"
	errorCodeTaskNotFound     = "task_not_found"
)

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Title string `json:"title"`
	}
	if err := decodeJSONRequest(w, r, &request); err != nil {
		if errors.Is(err, errInvalidJSONBody) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, invalidJSONBodyMessage)
			return
		}

		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	task, err := s.taskService.CreateTask(r.Context(), request.Title)
	if err != nil {
		writeTaskServiceError(w, err)
		return
	}

	w.Header().Set("Location", "/v1/tasks/"+url.PathEscape(task.ID))
	writeJSON(w, http.StatusCreated, newTaskEnvelope(task))
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.taskService.ListTasks(r.Context())
	if err != nil {
		writeTaskServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newTasksEnvelope(tasks))
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.taskService.GetTask(r.Context(), r.PathValue("task_id"))
	if err != nil {
		writeTaskServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newTaskEnvelope(task))
}

func (s *Server) completeTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.taskService.CompleteTask(r.Context(), r.PathValue("task_id"))
	if err != nil {
		writeTaskServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newTaskEnvelope(task))
}

func (s *Server) reopenTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.taskService.ReopenTask(r.Context(), r.PathValue("task_id"))
	if err != nil {
		writeTaskServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newTaskEnvelope(task))
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.taskService.DeleteTask(r.Context(), r.PathValue("task_id")); err != nil {
		writeTaskServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type taskPayload struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

type taskEnvelope struct {
	Task taskPayload `json:"task"`
}

type tasksEnvelope struct {
	Tasks []taskPayload `json:"tasks"`
}

func newTaskEnvelope(task app.TaskResult) taskEnvelope {
	return taskEnvelope{
		Task: newTaskPayload(task),
	}
}

func newTasksEnvelope(tasks []app.TaskResult) tasksEnvelope {
	payloads := make([]taskPayload, 0, len(tasks))
	for _, task := range tasks {
		payloads = append(payloads, newTaskPayload(task))
	}

	return tasksEnvelope{Tasks: payloads}
}

func newTaskPayload(task app.TaskResult) taskPayload {
	return taskPayload{
		ID:        task.ID,
		Title:     task.Title,
		Completed: task.Completed,
	}
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorPayload{
			Code:    code,
			Message: message,
		},
	})
}

func writeTaskServiceError(w http.ResponseWriter, err error) {
	switch app.ClassifyTaskError(err) {
	case app.TaskErrorInvalidTitle:
		writeError(w, http.StatusUnprocessableEntity, errorCodeInvalidTaskTitle, "invalid task title")
	case app.TaskErrorNotFound:
		writeError(w, http.StatusNotFound, errorCodeTaskNotFound, app.ErrTaskNotFound.Error())
	default:
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
	}
}

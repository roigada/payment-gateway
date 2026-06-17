package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/roigada/payment-gateway/internal/app"
)

type Server struct {
	handler   http.Handler
	logger    *slog.Logger
	tasks     taskUseCases
	readiness readinessChecker
}

type taskUseCases interface {
	CreateTask(ctx context.Context, title string) (app.TaskResult, error)
	ListTasks(ctx context.Context) ([]app.TaskResult, error)
	GetTask(ctx context.Context, id string) (app.TaskResult, error)
	CompleteTask(ctx context.Context, id string) (app.TaskResult, error)
	ReopenTask(ctx context.Context, id string) (app.TaskResult, error)
	DeleteTask(ctx context.Context, id string) error
}

type readinessChecker interface {
	CheckReady(ctx context.Context) error
}

func NewServer(tasks taskUseCases, readiness readinessChecker, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		logger:    logger,
		tasks:     tasks,
		readiness: readiness,
	}
	server.handler = server.routes()
	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("POST /v1/tasks", s.createTask)
	mux.HandleFunc("GET /v1/tasks", s.listTasks)
	mux.HandleFunc("GET /v1/tasks/{task_id}", s.getTask)
	mux.HandleFunc("POST /v1/tasks/{task_id}/complete", s.completeTask)
	mux.HandleFunc("POST /v1/tasks/{task_id}/reopen", s.reopenTask)
	mux.HandleFunc("DELETE /v1/tasks/{task_id}", s.deleteTask)

	return s.logRequest(s.recoverPanic(mux))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.readiness == nil {
		writeError(w, http.StatusServiceUnavailable, errorCodeServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
		return
	}
	if err := s.readiness.CheckReady(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, errorCodeServiceUnavailable, http.StatusText(http.StatusServiceUnavailable))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

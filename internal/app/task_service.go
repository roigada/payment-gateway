package app

import (
	"context"
	"errors"

	"github.com/roigada/template-go/internal/domain"
)

var ErrTaskNotFound = errors.New("task not found")

type TaskResult struct {
	ID        string
	Title     string
	Completed bool
}

type TaskRepository interface {
	Create(ctx context.Context, task *domain.Task) error
	Update(ctx context.Context, task *domain.Task) error
	FindByID(ctx context.Context, id domain.TaskID) (*domain.Task, error)
	List(ctx context.Context) ([]*domain.Task, error)
	Delete(ctx context.Context, id domain.TaskID) error
}

type TaskIDGenerator interface {
	NewTaskID() domain.TaskID
}

type TaskNotifier interface {
	NotifyTaskCreated(ctx context.Context, task TaskResult) error
	NotifyTaskCompleted(ctx context.Context, task TaskResult) error
}

type NoopTaskNotifier struct{}

func (NoopTaskNotifier) NotifyTaskCreated(context.Context, TaskResult) error {
	return nil
}

func (NoopTaskNotifier) NotifyTaskCompleted(context.Context, TaskResult) error {
	return nil
}

type TaskService struct {
	taskRepository TaskRepository
	ids            TaskIDGenerator
	notifier       TaskNotifier
}

func NewTaskService(taskRepository TaskRepository, ids TaskIDGenerator, notifier TaskNotifier) *TaskService {
	if notifier == nil {
		notifier = NoopTaskNotifier{}
	}

	return &TaskService{
		taskRepository: taskRepository,
		ids:            ids,
		notifier:       notifier,
	}
}

func (s *TaskService) CreateTask(ctx context.Context, title string) (TaskResult, error) {
	task, err := domain.NewTask(s.ids.NewTaskID(), title)
	if err != nil {
		return TaskResult{}, err
	}

	if err := s.taskRepository.Create(ctx, task); err != nil {
		return TaskResult{}, err
	}

	result := newTaskResult(task)
	if err := s.notifier.NotifyTaskCreated(ctx, result); err != nil {
		return TaskResult{}, err
	}

	return result, nil
}

func (s *TaskService) ListTasks(ctx context.Context) ([]TaskResult, error) {
	tasks, err := s.taskRepository.List(ctx)
	if err != nil {
		return nil, err
	}

	return newTaskResults(tasks), nil
}

func (s *TaskService) GetTask(ctx context.Context, id string) (TaskResult, error) {
	task, err := s.taskRepository.FindByID(ctx, domain.TaskID(id))
	if err != nil {
		return TaskResult{}, err
	}

	return newTaskResult(task), nil
}

func (s *TaskService) CompleteTask(ctx context.Context, id string) (TaskResult, error) {
	task, err := s.taskRepository.FindByID(ctx, domain.TaskID(id))
	if err != nil {
		return TaskResult{}, err
	}

	task.Complete()
	if err := s.taskRepository.Update(ctx, task); err != nil {
		return TaskResult{}, err
	}

	result := newTaskResult(task)
	if err := s.notifier.NotifyTaskCompleted(ctx, result); err != nil {
		return TaskResult{}, err
	}

	return result, nil
}

func (s *TaskService) ReopenTask(ctx context.Context, id string) (TaskResult, error) {
	task, err := s.taskRepository.FindByID(ctx, domain.TaskID(id))
	if err != nil {
		return TaskResult{}, err
	}

	task.Reopen()
	if err := s.taskRepository.Update(ctx, task); err != nil {
		return TaskResult{}, err
	}

	return newTaskResult(task), nil
}

func (s *TaskService) DeleteTask(ctx context.Context, id string) error {
	return s.taskRepository.Delete(ctx, domain.TaskID(id))
}

func newTaskResults(tasks []*domain.Task) []TaskResult {
	results := make([]TaskResult, 0, len(tasks))
	for _, task := range tasks {
		results = append(results, newTaskResult(task))
	}
	return results
}

func newTaskResult(task *domain.Task) TaskResult {
	return TaskResult{
		ID:        string(task.ID()),
		Title:     task.Title(),
		Completed: task.Completed(),
	}
}

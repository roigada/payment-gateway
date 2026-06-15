package testsupport

import (
	"context"
	"errors"

	"github.com/roigada/template-go/internal/app"
	"github.com/roigada/template-go/internal/domain"
)

type TaskRepository struct {
	tasks map[domain.TaskID]*domain.Task
}

func NewTaskRepository() *TaskRepository {
	return &TaskRepository{
		tasks: make(map[domain.TaskID]*domain.Task),
	}
}

func (r *TaskRepository) Create(_ context.Context, task *domain.Task) error {
	if _, ok := r.tasks[task.ID()]; ok {
		return errors.New("task already exists")
	}

	stored, err := cloneTask(task)
	if err != nil {
		return err
	}

	r.tasks[task.ID()] = stored
	return nil
}

func (r *TaskRepository) Update(_ context.Context, task *domain.Task) error {
	if _, ok := r.tasks[task.ID()]; !ok {
		return app.ErrTaskNotFound
	}

	stored, err := cloneTask(task)
	if err != nil {
		return err
	}

	r.tasks[task.ID()] = stored
	return nil
}

func (r *TaskRepository) FindByID(_ context.Context, id domain.TaskID) (*domain.Task, error) {
	task, ok := r.tasks[id]
	if !ok {
		return nil, app.ErrTaskNotFound
	}
	return cloneTask(task)
}

func (r *TaskRepository) List(_ context.Context) ([]*domain.Task, error) {
	tasks := make([]*domain.Task, 0, len(r.tasks))
	for _, task := range r.tasks {
		cloned, err := cloneTask(task)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, cloned)
	}
	return tasks, nil
}

func (r *TaskRepository) Delete(_ context.Context, id domain.TaskID) error {
	if _, ok := r.tasks[id]; !ok {
		return app.ErrTaskNotFound
	}
	delete(r.tasks, id)
	return nil
}

func cloneTask(task *domain.Task) (*domain.Task, error) {
	return domain.LoadTask(task.ID(), task.Title(), task.Completed())
}

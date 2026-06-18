package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
)

type TaskRepository struct {
	db *sql.DB
}

func NewTaskRepository(db *sql.DB) *TaskRepository {
	return &TaskRepository{db: db}
}

func (r *TaskRepository) Create(ctx context.Context, task *domain.Task) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO tasks (id, title, completed)
		 VALUES ($1, $2, $3)`,
		task.ID(),
		task.Title(),
		task.Completed(),
	)
	return err
}

func (r *TaskRepository) Update(ctx context.Context, task *domain.Task) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE tasks
		 SET title = $2,
		     completed = $3
		 WHERE id = $1`,
		task.ID(),
		task.Title(),
		task.Completed(),
	)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return app.ErrTaskNotFound
	}

	return nil
}

func (r *TaskRepository) FindByID(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	var title string
	var completed bool
	err := r.db.QueryRowContext(
		ctx,
		`SELECT title, completed FROM tasks WHERE id = $1`,
		id,
	).Scan(&title, &completed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, app.ErrTaskNotFound
		}
		return nil, err
	}

	return domain.LoadTask(id, title, completed)
}

func (r *TaskRepository) List(ctx context.Context) ([]*domain.Task, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, title, completed FROM tasks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*domain.Task
	for rows.Next() {
		var id domain.TaskID
		var title string
		var completed bool
		if err := rows.Scan(&id, &title, &completed); err != nil {
			return nil, err
		}
		task, err := domain.LoadTask(id, title, completed)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tasks, nil
}

func (r *TaskRepository) Delete(ctx context.Context, id domain.TaskID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return app.ErrTaskNotFound
	}

	return nil
}

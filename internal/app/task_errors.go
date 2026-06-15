package app

import (
	"errors"

	"github.com/roigada/template-go/internal/domain"
)

type TaskErrorKind string

const (
	TaskErrorInvalidTitle TaskErrorKind = "invalid_title"
	TaskErrorNotFound     TaskErrorKind = "not_found"
	TaskErrorUnknown      TaskErrorKind = "unknown"
)

func ClassifyTaskError(err error) TaskErrorKind {
	switch {
	case errors.Is(err, domain.ErrInvalidTaskTitle):
		return TaskErrorInvalidTitle
	case errors.Is(err, ErrTaskNotFound):
		return TaskErrorNotFound
	default:
		return TaskErrorUnknown
	}
}

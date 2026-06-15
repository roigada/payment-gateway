package uuidgen

import (
	"github.com/google/uuid"
	"github.com/roigada/template-go/internal/domain"
)

type TaskIDGenerator struct{}

func NewTaskIDGenerator() TaskIDGenerator {
	return TaskIDGenerator{}
}

func (TaskIDGenerator) NewTaskID() domain.TaskID {
	return domain.TaskID(uuid.NewString())
}

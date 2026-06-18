package testsupport

import "github.com/roigada/payment-gateway/internal/domain"

type FixedTaskIDGenerator struct {
	ID domain.TaskID
}

func (g FixedTaskIDGenerator) NewTaskID() domain.TaskID {
	return g.ID
}

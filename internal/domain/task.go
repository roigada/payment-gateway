package domain

import (
	"errors"
	"strings"
)

var ErrInvalidTaskTitle = errors.New("invalid task title")

type TaskID string

type Task struct {
	id        TaskID
	title     string
	completed bool
}

func NewTask(id TaskID, title string) (*Task, error) {
	title, err := normalizeTitle(title)
	if err != nil {
		return nil, err
	}

	return &Task{
		id:        id,
		title:     title,
		completed: false,
	}, nil
}

func LoadTask(id TaskID, title string, completed bool) (*Task, error) {
	title, err := normalizeTitle(title)
	if err != nil {
		return nil, err
	}

	return &Task{
		id:        id,
		title:     title,
		completed: completed,
	}, nil
}

func normalizeTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", ErrInvalidTaskTitle
	}

	return title, nil
}

func (t *Task) ID() TaskID {
	return t.id
}

func (t *Task) Title() string {
	return t.title
}

func (t *Task) Completed() bool {
	return t.completed
}

func (t *Task) Complete() {
	t.completed = true
}

func (t *Task) Reopen() {
	t.completed = false
}

package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSystemClockReturnsCurrentTime(t *testing.T) {
	before := time.Now()
	now := SystemClock{}.Now()
	after := time.Now()

	assert.False(t, now.Before(before))
	assert.False(t, now.After(after))
}

package app_test

import (
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/stretchr/testify/assert"
)

func TestSystemClockReturnsUTCTime(t *testing.T) {
	now := app.SystemClock{}.Now()

	assert.Equal(t, time.UTC, now.Location())
}

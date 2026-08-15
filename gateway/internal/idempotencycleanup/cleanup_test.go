package idempotencycleanup

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRunnerWaitsForTickRecordsOutcomesAndStopsWithContext(t *testing.T) {
	cleanupRuns := make(chan struct{}, 3)
	cleanupCalls := 0
	cleaner := cleanerFake{cleanup: func(_ context.Context) (int, error) {
		cleanupRuns <- struct{}{}
		cleanupCalls++
		switch cleanupCalls {
		case 1:
			return 0, assert.AnError
		case 2:
			return 0, nil
		default:
			return 3, nil
		}
	}}
	logs := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	testTicker := &tickerFake{ticks: make(chan time.Time, 3), awaitingTick: make(chan struct{}, 1)}
	metrics := &metricsFake{}
	runner := New(cleaner, metrics, slog.New(slog.NewJSONHandler(logs, nil)), time.Hour)
	runner.newTicker = func(time.Duration) ticker { return testTicker }
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()
	requireReceive(t, testTicker.awaitingTick)
	select {
	case <-cleanupRuns:
		t.Fatal("cleanup ran before the first ticker event")
	default:
	}

	testTicker.ticks <- time.Time{}
	testTicker.ticks <- time.Time{}
	testTicker.ticks <- time.Time{}
	requireReceive(t, cleanupRuns)
	requireReceive(t, cleanupRuns)
	requireReceive(t, cleanupRuns)
	cancel()
	requireReceive(t, done)
	assert.True(t, testTicker.stopped)
	assert.Equal(t, []metricCall{{result: failed}, {result: empty}, {result: completed, removed: 3}}, metrics.calls)
	assert.Contains(t, logs.String(), "idempotency replay cleanup failed")
	assert.Contains(t, logs.String(), "idempotency replay cleanup completed")
	assert.NotContains(t, logs.String(), assert.AnError.Error())
}

type cleanerFake struct {
	cleanup func(context.Context) (int, error)
}

func (f cleanerFake) CleanupCompletedIdempotencyReplays(ctx context.Context) (int, error) {
	return f.cleanup(ctx)
}

type tickerFake struct {
	ticks        chan time.Time
	awaitingTick chan struct{}
	stopped      bool
}

func (t *tickerFake) Chan() <-chan time.Time {
	select {
	case t.awaitingTick <- struct{}{}:
	default:
	}
	return t.ticks
}

func (t *tickerFake) Stop() { t.stopped = true }

type metricCall struct {
	result  string
	removed int
}

type metricsFake struct{ calls []metricCall }

func (f *metricsFake) RecordIdempotencyReplayCleanup(result string, removed int) {
	f.calls = append(f.calls, metricCall{result: result, removed: removed})
}

func requireReceive[T any](t *testing.T, received <-chan T) T {
	t.Helper()
	select {
	case value := <-received:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for value")
		var zero T
		return zero
	}
}

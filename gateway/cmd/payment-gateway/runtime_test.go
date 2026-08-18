package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Normal shutdown makes net/http return ServerClosed, but that is an expected lifecycle event;
// the runtime helper must convert it to nil so the process does not report a false failure.
func TestListenAndServeNormalizesServerClosed(t *testing.T) {
	server := &http.Server{Addr: "127.0.0.1:0"}
	_ = server.Shutdown(context.Background())
	results := make(chan error, 1)
	go listenAndServe(server, results)

	require.NoError(t, requireReceive(t, results))
}

// Readiness delegates normally until shutdown begins. During a drain it must immediately become
// unavailable without consulting its delegate, so load balancers stop sending new requests first.
func TestShutdownReadinessBecomesUnavailableWhenDraining(t *testing.T) {
	delegateErr := errors.New("database unavailable")
	delegate := &readinessCheckerFake{err: delegateErr}
	readiness := newShutdownReadiness(delegate)
	ctx := context.WithValue(context.Background(), readinessContextKey{}, "readiness")

	require.ErrorIs(t, readiness.CheckReady(ctx), delegateErr)
	assert.Same(t, ctx, delegate.ctx)
	assert.Equal(t, 1, delegate.calls)

	readiness.beginDrain()
	assert.EqualError(t, readiness.CheckReady(ctx), "payment-gateway is draining")
	assert.Equal(t, 1, delegate.calls)
}

// Graceful shutdown stops both public and metrics listeners from accepting new work but waits for
// an in-flight public request to finish, avoiding an interrupted response during normal shutdown.
func TestShutdownServersLetsActiveRequestsFinishAndStopsBothServers(t *testing.T) {
	requestStarted := make(chan struct{})
	finishRequest := make(chan struct{})
	publicServer, publicListener := newTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(requestStarted)
		<-finishRequest
	}))
	metricsServer, metricsListener := newTestServer(t, http.NotFoundHandler())
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + publicListener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	requireReceive(t, requestStarted)

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownServers(context.Background(), publicServer, metricsServer)
	}()
	assert.Never(t, func() bool {
		select {
		case <-shutdownDone:
			return true
		default:
			return false
		}
	}, 50*time.Millisecond, time.Millisecond)

	close(finishRequest)
	require.NoError(t, requireReceive(t, requestDone))
	require.NoError(t, requireReceive(t, shutdownDone))
	requireServerUnavailable(t, publicListener.Addr().String())
	requireServerUnavailable(t, metricsListener.Addr().String())
}

// Forced close stops both listeners immediately and cancels an in-flight request's context, so a
// handler can abandon work when the graceful-shutdown deadline has already been exhausted.
func TestCloseServersCancelsActiveRequestsAndStopsBothServers(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	publicServer, publicListener := newTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-request.Context().Done()
		close(requestCanceled)
	}))
	metricsServer, metricsListener := newTestServer(t, http.NotFoundHandler())
	go func() {
		response, _ := http.Get("http://" + publicListener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
	}()
	requireReceive(t, requestStarted)

	require.NoError(t, closeServers(publicServer, metricsServer))
	requireReceive(t, requestCanceled)
	requireServerUnavailable(t, publicListener.Addr().String())
	requireServerUnavailable(t, metricsListener.Addr().String())
}

func newTestServer(t *testing.T, handler http.Handler) (*http.Server, net.Listener) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return server, listener
}

func requireReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for result")
		var zero T
		return zero
	}
}

func requireServerUnavailable(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Get("http://" + addr)
	if response != nil {
		response.Body.Close()
	}
	require.Error(t, err)
}

type readinessContextKey struct{}

type readinessCheckerFake struct {
	ctx   context.Context
	err   error
	calls int
}

func (f *readinessCheckerFake) CheckReady(ctx context.Context) error {
	f.ctx = ctx
	f.calls++
	return f.err
}

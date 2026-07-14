package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

type readinessChecker interface {
	CheckReady(context.Context) error
}

type shutdownReadiness struct {
	delegate readinessChecker
	draining atomic.Bool
}

func newShutdownReadiness(delegate readinessChecker) *shutdownReadiness {
	return &shutdownReadiness{delegate: delegate}
}

func (r *shutdownReadiness) CheckReady(ctx context.Context) error {
	if r.draining.Load() {
		return errors.New("payment-gateway is draining")
	}
	return r.delegate.CheckReady(ctx)
}

func (r *shutdownReadiness) beginDrain() {
	r.draining.Store(true)
}

func runHTTPServer(listener net.Listener, handler http.Handler, readiness *shutdownReadiness, shutdownTimeout time.Duration, shutdownSignals <-chan os.Signal, logger *slog.Logger) error {
	return serveHTTPServer(listener, handler, readiness, &http.Server{Handler: handler}, shutdownTimeout, shutdownSignals, logger)
}

func runHTTPServerWithConfig(listener net.Listener, handler http.Handler, readiness *shutdownReadiness, cfg config, shutdownSignals <-chan os.Signal, logger *slog.Logger) error {
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout, ReadTimeout: cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout, IdleTimeout: cfg.HTTP.IdleTimeout,
	}
	return serveHTTPServer(listener, handler, readiness, server, cfg.Runtime.ShutdownTimeout, shutdownSignals, logger)
}

func serveHTTPServer(listener net.Listener, handler http.Handler, readiness *shutdownReadiness, server *http.Server, shutdownTimeout time.Duration, shutdownSignals <-chan os.Signal, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	serveResult := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveResult <- err
	}()

	select {
	case err := <-serveResult:
		return err
	case receivedSignal := <-shutdownSignals:
		logger.Info("payment-gateway shutdown signal received", "signal", receivedSignal.String())
	}

	readiness.beginDrain()
	logger.Info("payment-gateway shutdown drain started", "timeout", shutdownTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	shutdownResult := make(chan error, 1)
	go func() {
		shutdownResult <- server.Shutdown(ctx)
	}()

	select {
	case err := <-shutdownResult:
		cancel()
		if err == nil {
			logger.Info("payment-gateway shutdown completed")
			return <-serveResult
		}

		logger.Warn("payment-gateway shutdown drain timed out", "error", err)
	case receivedSignal := <-shutdownSignals:
		logger.Warn("payment-gateway shutdown force requested", "signal", receivedSignal.String())
		cancel()
		<-shutdownResult
	}

	if closeErr := server.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		return closeErr
	}
	logger.Warn("payment-gateway shutdown forced connections closed")
	return <-serveResult
}

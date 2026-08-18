package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/idempotencycleanup"
	"github.com/roigada/payment-gateway/internal/mockbank"
	"github.com/roigada/payment-gateway/internal/observability"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/roigada/payment-gateway/internal/serviceauth"
	"github.com/roigada/payment-gateway/internal/uuidgen"
)

func run(cfg config, logger *slog.Logger) error {
	dbCtx, cancel := context.WithTimeout(context.Background(), cfg.DatabaseStartupTimeout)
	defer cancel()

	db, err := postgres.Open(dbCtx, cfg.postgresConfig())
	if err != nil {
		return err
	}
	defer db.Close()

	readiness := newShutdownReadiness(postgres.NewReadinessChecker(db))
	paymentStore := postgres.NewPaymentStore(db)
	paymentClock := app.SystemClock{}
	metrics, err := observability.NewMetrics(db, paymentStore)
	if err != nil {
		return err
	}
	mockBank, err := mockbank.NewClient(metrics.MockBank, cfg.mockBankConfig())
	if err != nil {
		return err
	}
	paymentService := app.NewPaymentService(
		paymentStore,
		uuidgen.NewPaymentIDGenerator(),
		uuidgen.NewBankOperationKeyGenerator(),
		mockBank,
		metrics.PaymentOperations,
		paymentClock,
		cfg.FingerprintSecret,
		cfg.IdempotencyClaimStuckAfter,
	)

	authenticator, err := serviceauth.NewAuthenticator(cfg.ServiceCredentialHMACKey, cfg.ServiceCredentials)
	if err != nil {
		return err
	}
	handler, err := httpapi.NewHandler(paymentService, readiness, logger, metrics.HTTP, authenticator, app.SystemClock{}, cfg.handlerConfig())
	if err != nil {
		return err
	}
	cleanupRunner := idempotencycleanup.New(paymentService, metrics.IdempotencyReplayCleanup, logger, cfg.IdempotencyReplayCleanupInterval)
	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		cleanupRunner.Run(cleanupCtx)
	}()
	defer func() {
		cancelCleanup()
		<-cleanupDone
	}()

	publicServer := cfg.publicServer(handler)
	metricsServer := cfg.metricsServer(metrics.Handler)
	serveResults := make(chan error, 2)
	go listenAndServe(publicServer, serveResults)
	go listenAndServe(metricsServer, serveResults)

	shutdownSignals := make(chan os.Signal, 2)
	signal.Notify(shutdownSignals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdownSignals)

	logger.Info("payment-gateway starting", "addr", cfg.HTTPAddr, "metrics_addr", cfg.MetricsAddr)
	select {
	case err := <-serveResults:
		readiness.beginDrain()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		shutdownErr := shutdownServers(shutdownCtx, publicServer, metricsServer)
		cancel()
		if shutdownErr != nil {
			return shutdownErr
		}
		return err
	case receivedSignal := <-shutdownSignals:
		logger.Info("payment-gateway shutdown signal received", "signal", receivedSignal.String())
	}

	readiness.beginDrain()
	logger.Info("payment-gateway shutdown drain started", "timeout", cfg.ShutdownTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- shutdownServers(ctx, publicServer, metricsServer) }()

	select {
	case err := <-shutdownResult:
		cancel()
		if err == nil {
			logger.Info("payment-gateway shutdown completed")
			return <-serveResults
		}
		logger.Warn("payment-gateway shutdown drain timed out", "error", err)
	case receivedSignal := <-shutdownSignals:
		logger.Warn("payment-gateway shutdown force requested", "signal", receivedSignal.String())
		cancel()
		<-shutdownResult
	}

	if closeErr := closeServers(publicServer, metricsServer); closeErr != nil {
		return closeErr
	}
	logger.Warn("payment-gateway shutdown forced connections closed")
	return <-serveResults
}

func newHTTPServer(handler http.Handler, addr string, readHeaderTimeout, readTimeout, writeTimeout, idleTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr: addr, Handler: handler, ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: readTimeout,
		WriteTimeout: writeTimeout, IdleTimeout: idleTimeout,
	}
}

func listenAndServe(server *http.Server, results chan<- error) {
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	results <- err
}

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

func shutdownServers(ctx context.Context, publicServer, metricsServer *http.Server) error {
	results := make(chan error, 2)
	go func() { results <- publicServer.Shutdown(ctx) }()
	go func() { results <- metricsServer.Shutdown(ctx) }()
	firstErr, secondErr := <-results, <-results
	if firstErr != nil {
		return firstErr
	}
	return secondErr
}

func closeServers(publicServer, metricsServer *http.Server) error {
	publicErr := publicServer.Close()
	metricsErr := metricsServer.Close()
	if errors.Is(publicErr, http.ErrServerClosed) {
		publicErr = nil
	}
	if errors.Is(metricsErr, http.ErrServerClosed) {
		metricsErr = nil
	}
	return errors.Join(publicErr, metricsErr)
}

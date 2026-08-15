package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/idempotencycleanup"
	"github.com/roigada/payment-gateway/internal/mockbank"
	"github.com/roigada/payment-gateway/internal/observability"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/roigada/payment-gateway/internal/uuidgen"
)

func run(cfg config, logger *slog.Logger) error {
	dbCtx, cancel := context.WithTimeout(context.Background(), cfg.Database.StartupTimeout)
	defer cancel()

	db, err := postgres.Open(dbCtx, cfg.Database.postgresOptions())
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
	mockBank, err := mockbank.NewClient(metrics.MockBank, cfg.MockBank.clientConfig())
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
		cfg.Payment.FingerprintSecret,
		cfg.Payment.IdempotencyClaimStuckAfter,
	)
	httpRuntime, err := buildHTTPRuntime(readiness, logger, cfg.httpHandler(), paymentService, metrics)
	if err != nil {
		return err
	}
	cleanupRunner := idempotencycleanup.New(paymentService, metrics.IdempotencyReplayCleanup, logger, cfg.Runtime.IdempotencyReplayCleanupInterval)
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

	listener, err := net.Listen("tcp", cfg.HTTP.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	metricsListener, err := net.Listen("tcp", cfg.Metrics.Addr)
	if err != nil {
		return err
	}
	defer metricsListener.Close()

	shutdownSignals := make(chan os.Signal, 2)
	signal.Notify(shutdownSignals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdownSignals)

	logger.Info("payment-gateway starting", "addr", cfg.HTTP.Addr, "metrics_addr", cfg.Metrics.Addr)
	return serveUntilShutdownAll([]runtimeServer{{listener: listener, server: newHTTPServer(httpRuntime.handler, cfg.HTTP.ServerConfig)}, {listener: metricsListener, server: newHTTPServer(httpRuntime.metricsHandler, cfg.Metrics.ServerConfig)}}, readiness, cfg.Runtime.ShutdownTimeout, shutdownSignals, logger)
}

type httpRuntime struct {
	handler        http.Handler
	metricsHandler http.Handler
}

type httpAPIMetrics struct {
	*observability.HTTPMetrics
	*observability.RateLimitMetrics
}

func buildHTTPRuntime(readiness readinessChecker, logger *slog.Logger, cfg httpHandlerConfig, paymentService *app.PaymentService, metrics observability.Metrics) (httpRuntime, error) {
	authenticator, err := cfg.Auth.authenticator()
	if err != nil {
		return httpRuntime{}, err
	}
	handler, err := httpapi.NewHandler(httpapi.HandlerDependencies{
		Payments:      paymentService,
		Readiness:     readiness,
		Logger:        logger,
		Metrics:       httpAPIMetrics{HTTPMetrics: metrics.HTTP, RateLimitMetrics: metrics.RateLimit},
		Authenticator: authenticator,
		Clock:         app.SystemClock{},
	}, cfg.Options)
	if err != nil {
		return httpRuntime{}, err
	}
	return httpRuntime{handler: handler, metricsHandler: metrics.Handler}, nil
}

func newHTTPServer(handler http.Handler, cfg ServerConfig) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
	}
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

type runtimeServer struct {
	listener net.Listener
	server   *http.Server
}

func serveUntilShutdownAll(servers []runtimeServer, readiness *shutdownReadiness, shutdownTimeout time.Duration, shutdownSignals <-chan os.Signal, logger *slog.Logger) error {
	if logger == nil {
		return errors.New("runtime logger is required")
	}
	serveResult := make(chan error, len(servers))
	for _, runtimeServer := range servers {
		go func(server *http.Server, listener net.Listener) {
			err := server.Serve(listener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			serveResult <- err
		}(runtimeServer.server, runtimeServer.listener)
	}

	select {
	case err := <-serveResult:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		shutdownErr := shutdownServers(shutdownCtx, servers)
		cancel()
		if shutdownErr != nil {
			return shutdownErr
		}
		return err
	case receivedSignal := <-shutdownSignals:
		logger.Info("payment-gateway shutdown signal received", "signal", receivedSignal.String())
	}

	readiness.beginDrain()
	logger.Info("payment-gateway shutdown drain started", "timeout", shutdownTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- shutdownServers(ctx, servers) }()

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

	if closeErr := closeServers(servers); closeErr != nil {
		return closeErr
	}
	logger.Warn("payment-gateway shutdown forced connections closed")
	return <-serveResult
}

func shutdownServers(ctx context.Context, servers []runtimeServer) error {
	results := make(chan error, len(servers))
	var group sync.WaitGroup
	for _, runtimeServer := range servers {
		group.Add(1)
		go func(server *http.Server) {
			defer group.Done()
			results <- server.Shutdown(ctx)
		}(runtimeServer.server)
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			return err
		}
	}
	return nil
}

func closeServers(servers []runtimeServer) error {
	for _, runtimeServer := range servers {
		if err := runtimeServer.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

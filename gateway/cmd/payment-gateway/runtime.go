package main

import (
	"context"
	"database/sql"
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

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
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
	httpRuntime, err := buildHTTPRuntime(db, paymentStore, readiness, logger, cfg.httpHandler())
	if err != nil {
		return err
	}
	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		runIdempotencyReplayCleanup(cleanupCtx, paymentStore, app.SystemClock{}, logger, httpRuntime.cleanupMetrics, cfg.Runtime.IdempotencyReplayWindow, timeTicker{time.NewTicker(cfg.Runtime.IdempotencyReplayCleanupInterval)})
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
	return serveUntilShutdownAll([]runtimeServer{{listener: listener, server: newHTTPServer(httpRuntime.handler, cfg.HTTP)}, {listener: metricsListener, server: newHTTPServer(httpRuntime.metricsHandler, cfg.HTTP)}}, readiness, cfg.Runtime.ShutdownTimeout, shutdownSignals, logger, cancelCleanup)
}

type httpRuntime struct {
	handler        http.Handler
	metricsHandler http.Handler
	cleanupMetrics *observability.IdempotencyReplayCleanupMetrics
}

type httpAPIMetrics struct {
	*observability.HTTPMetrics
	*observability.RateLimitMetrics
}

func buildHTTPRuntime(db *sql.DB, paymentStore *postgres.PaymentStore, readiness readinessChecker, logger *slog.Logger, cfg httpHandlerConfig) (httpRuntime, error) {
	authenticator, err := cfg.Auth.authenticator()
	if err != nil {
		return httpRuntime{}, err
	}
	metricsRuntime, err := buildMetricsRuntime(db, paymentStore)
	if err != nil {
		return httpRuntime{}, err
	}

	mockBank, err := newMockBankClient(cfg.MockBank, metricsRuntime.mockBankMetrics)
	if err != nil {
		return httpRuntime{}, err
	}

	paymentService := app.NewPaymentService(paymentStore, uuidgen.NewPaymentIDGenerator(), uuidgen.NewBankOperationKeyGenerator(), mockBank, metricsRuntime.paymentOperationMetrics, app.SystemClock{}, cfg.Payment.FingerprintSecret, cfg.Payment.IdempotencyClaimStuckAfter)
	rateLimiter, err := httpapi.NewRateLimiter(app.SystemClock{}, cfg.RateLimit)
	if err != nil {
		return httpRuntime{}, err
	}
	handler, err := httpapi.NewHandler(httpapi.HandlerDependencies{
		Payments:      paymentService,
		Readiness:     readiness,
		Logger:        logger,
		Metrics:       httpAPIMetrics{HTTPMetrics: metricsRuntime.httpMetrics, RateLimitMetrics: metricsRuntime.rateLimitMetrics},
		Authenticator: authenticator,
		RateLimiter:   rateLimiter,
	}, cfg.Options)
	if err != nil {
		return httpRuntime{}, err
	}
	return httpRuntime{handler: handler, metricsHandler: metricsRuntime.handler, cleanupMetrics: metricsRuntime.cleanupMetrics}, nil
}

type metricsRuntime struct {
	handler                 http.Handler
	httpMetrics             *observability.HTTPMetrics
	rateLimitMetrics        *observability.RateLimitMetrics
	mockBankMetrics         *observability.MockBankMetrics
	paymentOperationMetrics *observability.PaymentOperationMetrics
	cleanupMetrics          *observability.IdempotencyReplayCleanupMetrics
}

func buildMetricsRuntime(db *sql.DB, paymentStore *postgres.PaymentStore) (metricsRuntime, error) {
	registry := observability.NewRegistry()
	httpMetrics, err := observability.NewHTTPMetrics(registry)
	if err != nil {
		return metricsRuntime{}, err
	}
	rateLimitMetrics, err := observability.NewRateLimitMetrics(registry)
	if err != nil {
		return metricsRuntime{}, err
	}
	mockBankMetrics, err := observability.NewMockBankMetrics(registry)
	if err != nil {
		return metricsRuntime{}, err
	}
	paymentOperationMetrics, err := observability.NewPaymentOperationMetrics(registry)
	if err != nil {
		return metricsRuntime{}, err
	}
	cleanupMetrics, err := observability.NewIdempotencyReplayCleanupMetrics(registry)
	if err != nil {
		return metricsRuntime{}, err
	}
	postgresPoolCollector, err := observability.NewPostgresPoolCollector(db)
	if err != nil {
		return metricsRuntime{}, err
	}
	if err := registry.Register(postgresPoolCollector); err != nil {
		return metricsRuntime{}, err
	}
	pendingPaymentCollector, err := observability.NewPendingPaymentCollector(paymentStore)
	if err != nil {
		return metricsRuntime{}, err
	}
	if err := registry.Register(pendingPaymentCollector); err != nil {
		return metricsRuntime{}, err
	}
	return metricsRuntime{
		handler:                 newMetricsHandler(promhttp.HandlerFor(registry, promhttp.HandlerOpts{})),
		httpMetrics:             httpMetrics,
		rateLimitMetrics:        rateLimitMetrics,
		mockBankMetrics:         mockBankMetrics,
		paymentOperationMetrics: paymentOperationMetrics,
		cleanupMetrics:          cleanupMetrics,
	}, nil
}

func newMockBankClient(cfg MockBankConfig, metrics *observability.MockBankMetrics) (*mockbank.Client, error) {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: cfg.ConnectTimeout}).DialContext,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		IdleConnTimeout:       cfg.IdleConnectionTimeout,
	}
	return mockbank.NewClient(cfg.BaseURL, &http.Client{Transport: transport}, metrics, mockbank.ClientConfig{
		Timeout:               cfg.Timeout,
		InitialAttemptTimeout: cfg.InitialAttemptTimeout,
		RetryDelay:            cfg.RetryDelay,
		RetryAttemptTimeout:   cfg.RetryAttemptTimeout,
	})
}

func newHTTPServer(handler http.Handler, cfg HTTPConfig) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
	}
}

func newMetricsHandler(metrics http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	return mux
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

func serveUntilShutdown(listener net.Listener, server *http.Server, readiness *shutdownReadiness, shutdownTimeout time.Duration, shutdownSignals <-chan os.Signal, logger *slog.Logger, onShutdownStart func()) error {
	return serveUntilShutdownAll([]runtimeServer{{listener: listener, server: server}}, readiness, shutdownTimeout, shutdownSignals, logger, onShutdownStart)
}

func serveUntilShutdownAll(servers []runtimeServer, readiness *shutdownReadiness, shutdownTimeout time.Duration, shutdownSignals <-chan os.Signal, logger *slog.Logger, onShutdownStart func()) error {
	if logger == nil {
		return errors.New("runtime logger is required")
	}
	activeRequestsCtx, cancelActiveRequests := context.WithCancel(context.Background())
	defer cancelActiveRequests()
	serveResult := make(chan error, len(servers))
	for _, runtimeServer := range servers {
		runtimeServer.server.BaseContext = func(net.Listener) context.Context { return activeRequestsCtx }
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

	if onShutdownStart != nil {
		onShutdownStart()
	}
	readiness.beginDrain()
	cancelActiveRequests()
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

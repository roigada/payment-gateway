package main

import (
	"net/url"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/mockbank"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/stretchr/testify/assert"
)

func TestConfigPostgresConfig(t *testing.T) {
	cfg := validConfig()
	cfg.DatabaseMaxOpenConnections = 20
	cfg.DatabaseMaxIdleConnections = 8
	cfg.DatabaseConnectionMaxLifetime = 45 * time.Minute
	cfg.DatabaseConnectionMaxIdleTime = 10 * time.Minute
	assert.Equal(t, postgres.Config{URL: cfg.DatabaseURL, MaxOpenConnections: 20, MaxIdleConnections: 8, ConnectionMaxLifetime: 45 * time.Minute, ConnectionMaxIdleTime: 10 * time.Minute}, cfg.postgresConfig())
}

func TestConfigMockBankConfig(t *testing.T) {
	cfg := validConfig()
	cfg.MockBankBaseURL = url.URL{Scheme: "https", Host: "mockbank.example"}
	cfg.MockBankTimeout = time.Minute
	cfg.MockBankInitialAttemptTimeout = 10 * time.Second
	cfg.MockBankRetryDelay = time.Second
	cfg.MockBankRetryAttemptTimeout = 5 * time.Second
	cfg.MockBankConnectTimeout = 2 * time.Second
	cfg.MockBankTLSHandshakeTimeout = 3 * time.Second
	cfg.MockBankResponseHeaderTimeout = 4 * time.Second
	cfg.MockBankIdleConnectionTimeout = 30 * time.Second

	assert.Equal(t, mockbank.Config{BaseURL: url.URL{Scheme: "https", Host: "mockbank.example"}, Timeout: time.Minute, InitialAttemptTimeout: 10 * time.Second, RetryDelay: time.Second, RetryAttemptTimeout: 5 * time.Second, ConnectTimeout: 2 * time.Second, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second, IdleConnectionTimeout: 30 * time.Second}, cfg.mockBankConfig())
}

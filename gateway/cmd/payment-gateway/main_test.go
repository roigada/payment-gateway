package main

import (
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/mockbank"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/stretchr/testify/assert"
)

func TestDatabaseConfigPostgresOptions(t *testing.T) {
	cfg := validConfig()
	cfg.Database.MaxOpenConnections = 20
	cfg.Database.MaxIdleConnections = 8
	cfg.Database.ConnectionMaxLifetime = 45 * time.Minute
	cfg.Database.ConnectionMaxIdleTime = 10 * time.Minute
	assert.Equal(t, postgres.Options{URL: cfg.Database.URL, MaxOpenConnections: 20, MaxIdleConnections: 8, ConnectionMaxLifetime: 45 * time.Minute, ConnectionMaxIdleTime: 10 * time.Minute}, cfg.Database.postgresOptions())
}

func TestMockBankConfigClientConfig(t *testing.T) {
	cfg := MockBankConfig{BaseURL: "https://mockbank.example", Timeout: time.Minute, InitialAttemptTimeout: 10 * time.Second, RetryDelay: time.Second, RetryAttemptTimeout: 5 * time.Second, ConnectTimeout: 2 * time.Second, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second, IdleConnectionTimeout: 30 * time.Second}

	assert.Equal(t, mockbank.ClientConfig{BaseURL: "https://mockbank.example", Timeout: time.Minute, InitialAttemptTimeout: 10 * time.Second, RetryDelay: time.Second, RetryAttemptTimeout: 5 * time.Second, ConnectTimeout: 2 * time.Second, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second, IdleConnectionTimeout: 30 * time.Second}, cfg.clientConfig())
}

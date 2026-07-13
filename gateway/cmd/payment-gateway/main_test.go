package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConfigureDatabasePoolAppliesConfig(t *testing.T) {
	pool := &recordingDatabasePool{}
	cfg := validConfig()
	cfg.DatabaseMaxOpenConnections = 20
	cfg.DatabaseMaxIdleConnections = 8
	cfg.DatabaseConnectionMaxLifetime = 45 * time.Minute
	configureDatabasePool(pool, cfg)
	assert.Equal(t, 20, pool.maxOpenConnections)
	assert.Equal(t, 8, pool.maxIdleConnections)
	assert.Equal(t, 45*time.Minute, pool.connectionMaxLifetime)
}

type recordingDatabasePool struct {
	maxOpenConnections, maxIdleConnections int
	connectionMaxLifetime                  time.Duration
}

func (p *recordingDatabasePool) SetMaxOpenConns(n int)              { p.maxOpenConnections = n }
func (p *recordingDatabasePool) SetMaxIdleConns(n int)              { p.maxIdleConnections = n }
func (p *recordingDatabasePool) SetConnMaxLifetime(d time.Duration) { p.connectionMaxLifetime = d }

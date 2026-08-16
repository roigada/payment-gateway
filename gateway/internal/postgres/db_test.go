package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConfigValidate(t *testing.T) {
	valid := Config{
		URL:                   "postgres://localhost/payment_gateway",
		MaxOpenConnections:    10,
		MaxIdleConnections:    5,
		ConnectionMaxLifetime: time.Minute,
		ConnectionMaxIdleTime: time.Minute,
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"URL", func(c *Config) { c.URL = "" }},
		{"max open connections", func(c *Config) { c.MaxOpenConnections = 0 }},
		{"max idle connections", func(c *Config) { c.MaxIdleConnections = -1 }},
		{"pool relationship", func(c *Config) { c.MaxIdleConnections = c.MaxOpenConnections + 1 }},
		{"connection max lifetime", func(c *Config) { c.ConnectionMaxLifetime = 0 }},
		{"connection max idle time", func(c *Config) { c.ConnectionMaxIdleTime = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := valid
			tt.mutate(&config)
			assert.Error(t, config.validate())
		})
	}
}

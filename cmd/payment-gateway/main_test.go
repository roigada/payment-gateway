package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigRequiresPaymentGatewayConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "database URL",
			env:     map[string]string{},
			wantErr: "DATABASE_URL is required",
		},
		{
			name: "mock bank base URL",
			env: map[string]string{
				"DATABASE_URL": "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable",
			},
			wantErr: "MOCK_BANK_BASE_URL is required",
		},
		{
			name: "authorization fingerprint secret",
			env: map[string]string{
				"DATABASE_URL":       "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable",
				"MOCK_BANK_BASE_URL": "http://localhost:9090",
			},
			wantErr: "AUTHORIZATION_FINGERPRINT_SECRET is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(func(key string) string {
				return tt.env[key]
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadConfigUsesPaymentGatewayRuntimeDefaults(t *testing.T) {
	cfg, err := loadConfig(func(key string) string {
		values := map[string]string{
			"DATABASE_URL":                     "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable",
			"MOCK_BANK_BASE_URL":               "http://localhost:9090",
			"AUTHORIZATION_FINGERPRINT_SECRET": "secret",
		}
		return values[key]
	})
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.Addr)
	assert.Equal(t, "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable", cfg.DatabaseURL)
	assert.Equal(t, "http://localhost:9090", cfg.MockBankBaseURL)
	assert.Equal(t, "secret", cfg.AuthorizationFingerprintSecret)
}

func TestLoadConfigAllowsCustomListenAddress(t *testing.T) {
	cfg, err := loadConfig(func(key string) string {
		values := map[string]string{
			"ADDR":                             ":9090",
			"DATABASE_URL":                     "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable",
			"MOCK_BANK_BASE_URL":               "http://localhost:9090",
			"AUTHORIZATION_FINGERPRINT_SECRET": "secret",
		}
		return values[key]
	})
	require.NoError(t, err)

	assert.Equal(t, ":9090", cfg.Addr)
}

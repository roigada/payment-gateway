package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidateRequiresPaymentGatewayConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config
		wantErr string
	}{
		{
			name:    "database URL",
			cfg:     config{},
			wantErr: "DATABASE_URL is required",
		},
		{
			name: "mock bank base URL",
			cfg: config{
				DatabaseURL: "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable",
			},
			wantErr: "MOCK_BANK_BASE_URL is required",
		},
		{
			name: "authorization fingerprint secret",
			cfg: config{
				DatabaseURL:     "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable",
				MockBankBaseURL: "http://localhost:9090",
			},
			wantErr: "AUTHORIZATION_FINGERPRINT_SECRET is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestConfigValidateAllowsPaymentGatewayConfiguration(t *testing.T) {
	cfg := config{
		DatabaseURL:                    "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable",
		MockBankBaseURL:                "http://localhost:9090",
		AuthorizationFingerprintSecret: "secret",
	}

	require.NoError(t, cfg.validate())
}

func TestLoadConfigUsesPaymentGatewayRuntimeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable")
	t.Setenv("MOCK_BANK_BASE_URL", "http://localhost:9090")
	t.Setenv("AUTHORIZATION_FINGERPRINT_SECRET", "secret")

	cfg := loadConfig()

	assert.Equal(t, ":8080", cfg.Addr)
	assert.Equal(t, "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable", cfg.DatabaseURL)
	assert.Equal(t, "http://localhost:9090", cfg.MockBankBaseURL)
	assert.Equal(t, "secret", cfg.AuthorizationFingerprintSecret)
}

func TestLoadConfigAllowsCustomListenAddress(t *testing.T) {
	t.Setenv("ADDR", ":9090")

	cfg := loadConfig()
	assert.Equal(t, ":9090", cfg.Addr)
}

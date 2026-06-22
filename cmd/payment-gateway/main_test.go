package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validDatabaseURL       = "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable"
	validMockBankBaseURL   = "http://localhost:9090"
	validFingerprintSecret = "secret"
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
				DatabaseURL: validDatabaseURL,
			},
			wantErr: "MOCK_BANK_BASE_URL is required",
		},
		{
			name: "fingerprint secret",
			cfg: config{
				DatabaseURL:     validDatabaseURL,
				MockBankBaseURL: validMockBankBaseURL,
			},
			wantErr: "FINGERPRINT_SECRET is required",
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
		DatabaseURL:       validDatabaseURL,
		MockBankBaseURL:   validMockBankBaseURL,
		FingerprintSecret: validFingerprintSecret,
	}

	require.NoError(t, cfg.validate())
}

func TestLoadConfigUsesPaymentGatewayRuntimeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", validDatabaseURL)
	t.Setenv("MOCK_BANK_BASE_URL", validMockBankBaseURL)
	t.Setenv("FINGERPRINT_SECRET", validFingerprintSecret)

	cfg := loadConfig()

	assert.Equal(t, ":8080", cfg.Addr)
	assert.Equal(t, validDatabaseURL, cfg.DatabaseURL)
	assert.Equal(t, validMockBankBaseURL, cfg.MockBankBaseURL)
	assert.Equal(t, validFingerprintSecret, cfg.FingerprintSecret)
}

func TestLoadConfigAllowsCustomListenAddress(t *testing.T) {
	t.Setenv("ADDR", ":9090")

	cfg := loadConfig()
	assert.Equal(t, ":9090", cfg.Addr)
}

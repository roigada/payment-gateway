package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigRequiresDatabaseURL(t *testing.T) {
	_, err := loadConfig(env(map[string]string{
		"MOCK_BANK_BASE_URL":               "https://mock-bank.test",
		"AUTHORIZATION_FINGERPRINT_SECRET": "secret",
	}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL is required")
}

func TestLoadConfigRequiresMockBankBaseURL(t *testing.T) {
	_, err := loadConfig(env(map[string]string{
		"DATABASE_URL":                     "postgres://paymentgateway:paymentgateway@localhost:5432/paymentgateway?sslmode=disable",
		"AUTHORIZATION_FINGERPRINT_SECRET": "secret",
	}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MOCK_BANK_BASE_URL is required")
}

func TestLoadConfigRequiresAuthorizationFingerprintSecret(t *testing.T) {
	_, err := loadConfig(env(map[string]string{
		"DATABASE_URL":       "postgres://paymentgateway:paymentgateway@localhost:5432/paymentgateway?sslmode=disable",
		"MOCK_BANK_BASE_URL": "https://mock-bank.test",
	}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTHORIZATION_FINGERPRINT_SECRET is required")
}

func TestLoadConfigDefaultsListenAddress(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{
		"DATABASE_URL":                     "postgres://paymentgateway:paymentgateway@localhost:5432/paymentgateway?sslmode=disable",
		"MOCK_BANK_BASE_URL":               "https://mock-bank.test",
		"AUTHORIZATION_FINGERPRINT_SECRET": "secret",
	}))
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.Addr)
}

func TestLoadConfigUsesConfiguredListenAddress(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{
		"DATABASE_URL":                     "postgres://paymentgateway:paymentgateway@localhost:5432/paymentgateway?sslmode=disable",
		"MOCK_BANK_BASE_URL":               "https://mock-bank.test",
		"AUTHORIZATION_FINGERPRINT_SECRET": "secret",
		"ADDR":                             ":9090",
	}))
	require.NoError(t, err)

	assert.Equal(t, ":9090", cfg.Addr)
}

func env(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

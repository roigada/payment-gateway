package observability

import (
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that postgres pool collector reports database stats.
func TestPostgresPoolCollectorReportsDatabaseStats(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	db.SetMaxOpenConns(10)

	registry := prometheus.NewRegistry()
	collector, err := NewPostgresPoolCollector(db)
	require.NoError(t, err)
	require.NoError(t, registry.Register(collector))

	families, err := registry.Gather()
	require.NoError(t, err)

	assert.Equal(t, float64(10), metricFamilyByName(t, families, "payment_gateway_postgres_pool_max_open_connections").GetMetric()[0].GetGauge().GetValue())
	assert.NotNil(t, metricFamilyByName(t, families, "payment_gateway_postgres_pool_open_connections"))
	assert.NotNil(t, metricFamilyByName(t, families, "payment_gateway_postgres_pool_in_use_connections"))
	assert.NotNil(t, metricFamilyByName(t, families, "payment_gateway_postgres_pool_idle_connections"))
	assert.NotNil(t, metricFamilyByName(t, families, "payment_gateway_postgres_pool_wait_count_total"))
	assert.NotNil(t, metricFamilyByName(t, families, "payment_gateway_postgres_pool_wait_duration_seconds_total"))
	assert.NotNil(t, metricFamilyByName(t, families, "payment_gateway_postgres_pool_max_idle_closed_total"))
	assert.NotNil(t, metricFamilyByName(t, families, "payment_gateway_postgres_pool_max_lifetime_closed_total"))
	assert.NotNil(t, metricFamilyByName(t, families, "payment_gateway_postgres_pool_max_idle_time_closed_total"))
}

// Verifies that new postgres pool collector requires database.
func TestNewPostgresPoolCollectorRequiresDatabase(t *testing.T) {
	collector, err := NewPostgresPoolCollector(nil)

	require.Error(t, err)
	assert.Nil(t, collector)
	assert.Contains(t, err.Error(), "postgres db is required")
}

package observability

import (
	"database/sql"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

type PostgresPoolCollector struct {
	db *sql.DB

	openConnections    *prometheus.Desc
	inUseConnections   *prometheus.Desc
	idleConnections    *prometheus.Desc
	maxOpenConnections *prometheus.Desc
	waitCount          *prometheus.Desc
	waitDuration       *prometheus.Desc
	maxIdleClosed      *prometheus.Desc
	maxLifetimeClosed  *prometheus.Desc
	maxIdleTimeClosed  *prometheus.Desc
}

func NewPostgresPoolCollector(db *sql.DB) (*PostgresPoolCollector, error) {
	if db == nil {
		return nil, fmt.Errorf("postgres db is required")
	}

	return &PostgresPoolCollector{
		db: db,
		openConnections: prometheus.NewDesc(
			"payment_gateway_postgres_pool_open_connections",
			"Number of established connections in the gateway Postgres connection pool.",
			nil,
			nil,
		),
		inUseConnections: prometheus.NewDesc(
			"payment_gateway_postgres_pool_in_use_connections",
			"Number of connections currently in use from the gateway Postgres connection pool.",
			nil,
			nil,
		),
		idleConnections: prometheus.NewDesc(
			"payment_gateway_postgres_pool_idle_connections",
			"Number of idle connections in the gateway Postgres connection pool.",
			nil,
			nil,
		),
		maxOpenConnections: prometheus.NewDesc(
			"payment_gateway_postgres_pool_max_open_connections",
			"Maximum number of open connections allowed in the gateway Postgres connection pool.",
			nil,
			nil,
		),
		waitCount: prometheus.NewDesc(
			"payment_gateway_postgres_pool_wait_count_total",
			"Total number of times a caller waited for a connection from the gateway Postgres connection pool.",
			nil,
			nil,
		),
		waitDuration: prometheus.NewDesc(
			"payment_gateway_postgres_pool_wait_duration_seconds_total",
			"Total time callers spent waiting for a connection from the gateway Postgres connection pool.",
			nil,
			nil,
		),
		maxIdleClosed: prometheus.NewDesc(
			"payment_gateway_postgres_pool_max_idle_closed_total",
			"Total number of connections closed because the gateway Postgres connection pool exceeded its idle connection limit.",
			nil,
			nil,
		),
		maxLifetimeClosed: prometheus.NewDesc(
			"payment_gateway_postgres_pool_max_lifetime_closed_total",
			"Total number of connections closed because they exceeded the gateway Postgres connection maximum lifetime.",
			nil,
			nil,
		),
		maxIdleTimeClosed: prometheus.NewDesc(
			"payment_gateway_postgres_pool_max_idle_time_closed_total",
			"Total number of connections closed because they exceeded the gateway Postgres connection maximum idle time.",
			nil,
			nil,
		),
	}, nil
}

func (c *PostgresPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.openConnections
	ch <- c.inUseConnections
	ch <- c.idleConnections
	ch <- c.maxOpenConnections
	ch <- c.waitCount
	ch <- c.waitDuration
	ch <- c.maxIdleClosed
	ch <- c.maxLifetimeClosed
	ch <- c.maxIdleTimeClosed
}

func (c *PostgresPoolCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.db.Stats()

	ch <- prometheus.MustNewConstMetric(c.openConnections, prometheus.GaugeValue, float64(stats.OpenConnections))
	ch <- prometheus.MustNewConstMetric(c.inUseConnections, prometheus.GaugeValue, float64(stats.InUse))
	ch <- prometheus.MustNewConstMetric(c.idleConnections, prometheus.GaugeValue, float64(stats.Idle))
	ch <- prometheus.MustNewConstMetric(c.maxOpenConnections, prometheus.GaugeValue, float64(stats.MaxOpenConnections))
	ch <- prometheus.MustNewConstMetric(c.waitCount, prometheus.CounterValue, float64(stats.WaitCount))
	ch <- prometheus.MustNewConstMetric(c.waitDuration, prometheus.CounterValue, stats.WaitDuration.Seconds())
	ch <- prometheus.MustNewConstMetric(c.maxIdleClosed, prometheus.CounterValue, float64(stats.MaxIdleClosed))
	ch <- prometheus.MustNewConstMetric(c.maxLifetimeClosed, prometheus.CounterValue, float64(stats.MaxLifetimeClosed))
	ch <- prometheus.MustNewConstMetric(c.maxIdleTimeClosed, prometheus.CounterValue, float64(stats.MaxIdleTimeClosed))
}

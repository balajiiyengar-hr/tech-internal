package metrics

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/extra/redisprometheus/v9"
	"github.com/redis/go-redis/v9"
)

const clientMetricsNamespace = "tech_internal_api"

// service is the constant "service" tag published on every client and HTTP
// metric so a shared Prometheus/Grafana instance can group and filter
// dashboards per service. Set once at startup via SetServiceName.
var service atomic.Value

func init() {
	service.Store("")
}

// SetServiceName publishes the service tag used by all metrics in this
// package. Call once at startup, before traffic starts.
func SetServiceName(name string) {
	service.Store(name)
}

// ServiceName returns the currently published service tag.
func ServiceName() string {
	return service.Load().(string)
}

var (
	postgresQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: clientMetricsNamespace,
		Subsystem: "postgres",
		Name:      "query_duration_seconds",
		Help:      "Postgres query duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"service"})

	redisCommandDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: clientMetricsNamespace,
		Subsystem: "redis",
		Name:      "command_duration_seconds",
		Help:      "Redis command duration in seconds, grouped into a bounded command set.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"command", "service"})

	cacheRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: clientMetricsNamespace,
		Subsystem: "cache",
		Name:      "requests_total",
		Help:      "Cache lookup results for bounded authentication cache types.",
	}, []string{"cache", "result", "service"})

	postgresStats = &postgresPoolCollector{
		connections: prometheus.NewDesc(
			prometheus.BuildFQName(clientMetricsNamespace, "postgres", "pool_connections"),
			"Current Postgres pool connections by state.",
			[]string{"state"}, nil,
		),
		acquires: prometheus.NewDesc(
			prometheus.BuildFQName(clientMetricsNamespace, "postgres", "pool_acquires_total"),
			"Total successful Postgres pool acquires.",
			nil, nil,
		),
		acquireDuration: prometheus.NewDesc(
			prometheus.BuildFQName(clientMetricsNamespace, "postgres", "pool_acquire_duration_seconds_total"),
			"Cumulative time spent acquiring Postgres pool connections.",
			nil, nil,
		),
		emptyAcquires: prometheus.NewDesc(
			prometheus.BuildFQName(clientMetricsNamespace, "postgres", "pool_empty_acquires_total"),
			"Total Postgres acquires that waited for an available connection.",
			nil, nil,
		),
		canceledAcquires: prometheus.NewDesc(
			prometheus.BuildFQName(clientMetricsNamespace, "postgres", "pool_canceled_acquires_total"),
			"Total canceled Postgres pool acquires.",
			nil, nil,
		),
	}

	redisStats = &redisStatsGetter{}
)

func init() {
	prometheus.MustRegister(postgresStats)
	prometheus.MustRegister(redisprometheus.NewCollector(clientMetricsNamespace, "redis", redisStats))
}

// PostgresQueryTracer records aggregate query latency without SQL text labels.
type PostgresQueryTracer struct{}

type queryStartKey struct{}

func (PostgresQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryStartKey{}, time.Now())
}

func (PostgresQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if start, ok := ctx.Value(queryStartKey{}).(time.Time); ok {
		postgresQueryDuration.WithLabelValues(ServiceName()).Observe(time.Since(start).Seconds())
	}
}

// SetPostgresPool selects the pool whose live statistics are exported.
func SetPostgresPool(pool *pgxpool.Pool) {
	postgresStats.mu.Lock()
	postgresStats.pool = pool
	postgresStats.mu.Unlock()
}

type postgresPoolCollector struct {
	mu               sync.RWMutex
	pool             *pgxpool.Pool
	connections      *prometheus.Desc
	acquires         *prometheus.Desc
	acquireDuration  *prometheus.Desc
	emptyAcquires    *prometheus.Desc
	canceledAcquires *prometheus.Desc
}

func (c *postgresPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connections
	ch <- c.acquires
	ch <- c.acquireDuration
	ch <- c.emptyAcquires
	ch <- c.canceledAcquires
}

func (c *postgresPoolCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	pool := c.pool
	c.mu.RUnlock()
	if pool == nil {
		return
	}

	stats := pool.Stat()
	for state, value := range map[string]float64{
		"idle":   float64(stats.IdleConns()),
		"in_use": float64(stats.AcquiredConns()),
		"total":  float64(stats.TotalConns()),
		"max":    float64(stats.MaxConns()),
	} {
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, value, state)
	}
	ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(stats.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.acquireDuration, prometheus.CounterValue, stats.AcquireDuration().Seconds())
	ch <- prometheus.MustNewConstMetric(c.emptyAcquires, prometheus.CounterValue, float64(stats.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.canceledAcquires, prometheus.CounterValue, float64(stats.CanceledAcquireCount()))
}

// InstrumentRedis adds bounded command timing and official go-redis pool metrics.
func InstrumentRedis(client *redis.Client) {
	redisStats.mu.Lock()
	redisStats.client = client
	redisStats.mu.Unlock()
	client.AddHook(redisMetricsHook{})
}

// ObserveCacheRequest records a lookup for a bounded authentication cache type.
func ObserveCacheRequest(cache, result string) {
	cacheRequests.WithLabelValues(cache, result, ServiceName()).Inc()
}

type redisStatsGetter struct {
	mu     sync.RWMutex
	client *redis.Client
}

func (g *redisStatsGetter) PoolStats() *redis.PoolStats {
	g.mu.RLock()
	client := g.client
	g.mu.RUnlock()
	if client == nil {
		return &redis.PoolStats{}
	}
	return client.PoolStats()
}

type redisMetricsHook struct{}

func (redisMetricsHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (redisMetricsHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		redisCommandDuration.WithLabelValues(redisCommandLabel(cmd.Name()), ServiceName()).Observe(time.Since(start).Seconds())
		return err
	}
}

func (redisMetricsHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		redisCommandDuration.WithLabelValues("pipeline", ServiceName()).Observe(time.Since(start).Seconds())
		return err
	}
}

func redisCommandLabel(name string) string {
	switch strings.ToLower(name) {
	case "get", "getdel", "set", "del":
		return strings.ToLower(name)
	default:
		return "other"
	}
}

package metrics

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
)

func TestObserveCacheRequest(t *testing.T) {
	counter := cacheRequests.WithLabelValues("identity", "hit")
	before := &dto.Metric{}
	if err := counter.Write(before); err != nil {
		t.Fatal(err)
	}
	ObserveCacheRequest("identity", "hit")
	after := &dto.Metric{}
	if err := counter.Write(after); err != nil {
		t.Fatal(err)
	}
	if got, want := after.GetCounter().GetValue(), before.GetCounter().GetValue()+1; got != want {
		t.Fatalf("cache counter=%v want %v", got, want)
	}
}

func TestRedisCommandLabelsAreBounded(t *testing.T) {
	for input, want := range map[string]string{
		"GET":     "get",
		"getdel":  "getdel",
		"SET":     "set",
		"del":     "del",
		"ping":    "other",
		"unknown": "other",
	} {
		if got := redisCommandLabel(input); got != want {
			t.Errorf("redisCommandLabel(%q)=%q want %q", input, got, want)
		}
	}
}

func TestRedisInstrumentationPublishesCommandAndPoolMetrics(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	InstrumentRedis(client)

	ctx := context.Background()
	if err := client.Set(ctx, "sensitive-key", "value", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(ctx, "sensitive-key").Err(); err != nil {
		t.Fatal(err)
	}

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	assertMetricFamily(t, families, "tech_internal_api_redis_command_duration_seconds")
	assertMetricFamily(t, families, "tech_internal_api_redis_pool_conn_total_current")
}

func assertMetricFamily(t *testing.T, families []*dto.MetricFamily, name string) {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return
		}
	}
	t.Fatalf("metric family %q not found", name)
}

func TestClientMetricAdapters(t *testing.T) {
	tracer := PostgresQueryTracer{}
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	tracer.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{})

	collector := &postgresPoolCollector{
		connections: postgresStats.connections, acquires: postgresStats.acquires,
		acquireDuration: postgresStats.acquireDuration, emptyAcquires: postgresStats.emptyAcquires,
		canceledAcquires: postgresStats.canceledAcquires,
	}
	descriptions := make(chan *prometheus.Desc, 5)
	collector.Describe(descriptions)
	if len(descriptions) != 5 {
		t.Fatalf("descriptions=%d", len(descriptions))
	}
	metrics := make(chan prometheus.Metric, 16)
	collector.Collect(metrics)
	if len(metrics) != 0 {
		t.Fatal("nil pool produced metrics")
	}
	pool, err := pgxpool.New(context.Background(), "postgres://127.0.0.1:1/test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	SetPostgresPool(pool)
	postgresStats.Collect(metrics)
	if len(metrics) != 8 {
		t.Fatalf("pool metrics=%d", len(metrics))
	}

	var getter redisStatsGetter
	if getter.PoolStats() == nil {
		t.Fatal("nil redis stats")
	}
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	getter.client = client
	if getter.PoolStats() == nil {
		t.Fatal("client redis stats")
	}
	InstrumentRedis(client)
	if _, err := client.Pipelined(context.Background(), func(p redis.Pipeliner) error {
		p.Set(context.Background(), "a", "b", 0)
		p.Get(context.Background(), "a")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hook := redisMetricsHook{}
	called := false
	dial := hook.DialHook(func(context.Context, string, string) (net.Conn, error) {
		called = true
		return nil, errors.New("dial")
	})
	_, _ = dial(context.Background(), "tcp", "address")
	if !called {
		t.Fatal("dial hook did not delegate")
	}
}

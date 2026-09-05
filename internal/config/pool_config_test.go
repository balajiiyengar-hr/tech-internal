package config

import "testing"

func TestLoadRepairsInvalidPoolAndCacheSettings(t *testing.T) {
	t.Setenv("DB_MAX_CONNS", "0")
	t.Setenv("DB_MIN_CONNS", "-1")
	t.Setenv("REDIS_POOL_SIZE", "0")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "-1")
	t.Setenv("MAX_INFLIGHT_DB", "0")
	t.Setenv("AUTH_CACHE_SIZE", "0")

	cfg := Load()
	if cfg.PoolMaxConns != 50 || cfg.PoolMinConns != 10 {
		t.Fatalf("database pool defaults = %d/%d", cfg.PoolMaxConns, cfg.PoolMinConns)
	}
	if cfg.RedisPoolSize != 100 || cfg.RedisMinIdle != 20 {
		t.Fatalf("Redis pool defaults = %d/%d", cfg.RedisPoolSize, cfg.RedisMinIdle)
	}
	if cfg.MaxInflightDB != 40 || cfg.AuthCacheSize != 5000 {
		t.Fatalf("cache limits = %d/%d", cfg.MaxInflightDB, cfg.AuthCacheSize)
	}
}

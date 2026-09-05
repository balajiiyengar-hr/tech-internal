package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	appcrypto "tech-internal/internal/crypto"
)

type Config struct {
	Port            string
	DatabaseURL     string
	RedisURL        string
	JWTSecret       string
	EncryptionKey   []byte
	JWTExpiry       time.Duration
	RefreshExpiry   time.Duration
	OTPExpiry       time.Duration
	PoolMaxConns    int32
	PoolMinConns    int32
	PoolMaxLifetime time.Duration
	RedisPoolSize   int
	RedisMinIdle    int
	MaxInflightDB   int
	AuthCacheSize   int
	ServiceName     string
	Environment     string
	CORSOrigins     []string
	DevLogOTP       bool
	PasswordKDF     string
}

func Load() Config {
	accessHours, _ := strconv.Atoi(getEnv("ACCESS_TOKEN_HOURS", ""))
	if accessHours <= 0 {
		accessHours, _ = strconv.Atoi(getEnv("JWT_EXPIRY_HOURS", "24"))
	}
	if accessHours <= 0 {
		accessHours = 24
	}
	refreshHours, _ := strconv.Atoi(getEnv("REFRESH_TOKEN_HOURS", "96"))
	if refreshHours <= 0 {
		refreshHours = 96
	}
	maxConns, _ := strconv.Atoi(getEnv("DB_MAX_CONNS", "50"))
	minConns, _ := strconv.Atoi(getEnv("DB_MIN_CONNS", "10"))
	maxLife, _ := strconv.Atoi(getEnv("DB_MAX_CONN_LIFETIME_MIN", "30"))
	redisPoolSize, _ := strconv.Atoi(getEnv("REDIS_POOL_SIZE", "100"))
	redisMinIdle, _ := strconv.Atoi(getEnv("REDIS_MIN_IDLE_CONNS", "20"))
	maxInflightDB, _ := strconv.Atoi(getEnv("MAX_INFLIGHT_DB", "40"))
	authCacheSize, _ := strconv.Atoi(getEnv("AUTH_CACHE_SIZE", "5000"))
	if maxConns <= 0 {
		maxConns = 50
	}
	if minConns < 0 || minConns > maxConns {
		minConns = 10
	}
	if redisPoolSize <= 0 {
		redisPoolSize = 100
	}
	if redisMinIdle < 0 || redisMinIdle > redisPoolSize {
		redisMinIdle = 20
	}
	if maxInflightDB <= 0 {
		maxInflightDB = 40
	}
	if authCacheSize <= 0 {
		authCacheSize = 5000
	}

	return Config{
		Port:            getEnv("PORT", "8080"),
		DatabaseURL:     getEnv("DATABASE_URL", "postgres://portal:portal@localhost:5434/tech_internal?sslmode=disable"),
		RedisURL:        getEnv("REDIS_URL", "redis://localhost:6380/0"),
		JWTSecret:       getEnv("JWT_SECRET", "tech-internal-dev-secret-change-in-production"),
		EncryptionKey:   appcrypto.ParseKey(getEnv("DATA_ENCRYPTION_KEY", "tech-internal-dev-data-key-change-me")),
		JWTExpiry:       time.Duration(accessHours) * time.Hour,
		RefreshExpiry:   time.Duration(refreshHours) * time.Hour,
		OTPExpiry:       120 * time.Second,
		PoolMaxConns:    int32(maxConns),
		PoolMinConns:    int32(minConns),
		PoolMaxLifetime: time.Duration(maxLife) * time.Minute,
		RedisPoolSize:   redisPoolSize,
		RedisMinIdle:    redisMinIdle,
		MaxInflightDB:   maxInflightDB,
		AuthCacheSize:   authCacheSize,
		ServiceName:     getEnv("SERVICE_NAME", "tech-internal-api"),
		Environment:     getEnv("APP_ENV", "dev"),
		CORSOrigins:     parseOrigins(getEnv("CORS_ORIGINS", "*")),
		DevLogOTP:       strings.EqualFold(getEnv("DEV_LOG_OTP", "false"), "true"),
		PasswordKDF:     strings.ToLower(getEnv("PASSWORD_KDF", "argon2id")),
	}
}

func parseOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	var origins []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			origins = append(origins, strings.TrimRight(p, "/"))
		}
	}
	if len(origins) == 0 {
		return []string{"*"}
	}
	return origins
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

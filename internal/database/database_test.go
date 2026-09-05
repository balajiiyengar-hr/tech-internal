package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tech-internal/internal/config"

	"github.com/alicebob/miniredis/v2"
	"github.com/pashagolub/pgxmock/v4"
)

func TestConnectionConfiguration(t *testing.T) {
	if _, err := NewPool(context.Background(), config.Config{DatabaseURL: "://bad"}); err == nil || !strings.Contains(err.Error(), "parse database url") {
		t.Fatalf("pool parse error=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := NewPool(ctx, config.Config{DatabaseURL: "postgres://127.0.0.1:1/db"}); err == nil {
		t.Fatal("expected database connection error")
	}
	if _, err := NewRedis(context.Background(), config.Config{RedisURL: "://bad"}); err == nil || !strings.Contains(err.Error(), "parse redis url") {
		t.Fatalf("redis parse error=%v", err)
	}
	redisCtx, redisCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer redisCancel()
	if _, err := NewRedis(redisCtx, config.Config{RedisURL: "redis://127.0.0.1:1"}); err == nil {
		t.Fatal("expected redis connection error")
	}
	server := miniredis.RunT(t)
	client, err := NewRedis(context.Background(), config.Config{RedisURL: "redis://" + server.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
}

func TestRunMigrations(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(ctx, mock, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing directory accepted")
	}
	empty := t.TempDir()
	os.WriteFile(filepath.Join(empty, "README"), []byte("x"), 0o600)
	os.Mkdir(filepath.Join(empty, "nested.sql"), 0o700)
	if err := RunMigrations(ctx, mock, empty); err == nil {
		t.Fatal("empty migration directory accepted")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "002.sql"), []byte("SECOND"), 0o600)
	os.WriteFile(filepath.Join(dir, "001.sql"), []byte("FIRST"), 0o600)
	mock.ExpectExec("FIRST").WillReturnResult(pgxmock.NewResult("OK", 0))
	mock.ExpectExec("SECOND").WillReturnResult(pgxmock.NewResult("OK", 0))
	if err := RunMigrations(ctx, mock, dir); err != nil {
		t.Fatal(err)
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "001.sql"), []byte("BAD"), 0o600)
	mock.ExpectExec("BAD").WillReturnError(errors.New("db"))
	if err := RunMigrations(ctx, mock, bad); err == nil || !strings.Contains(err.Error(), "run migration") {
		t.Fatalf("exec error=%v", err)
	}
}

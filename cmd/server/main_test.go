package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"tech-internal/internal/auth"
	"tech-internal/internal/config"
	"tech-internal/internal/handlers"
	"tech-internal/internal/models"
	"tech-internal/internal/repository"

	"github.com/alicebob/miniredis/v2"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
)

type fakeSeedUsers struct {
	user      *models.User
	getErr    error
	updateErr error
	updated   string
}

func (f *fakeSeedUsers) Get(context.Context, string, string, string) (*models.User, error) {
	return f.user, f.getErr
}
func (f *fakeSeedUsers) UpdatePasswordHash(_ context.Context, _, _, _, hash string) error {
	f.updated = hash
	return f.updateErr
}

func TestBuildRouter(t *testing.T) {
	cfg := config.Config{CORSOrigins: []string{"*"}, JWTSecret: "secret", JWTExpiry: time.Hour, RefreshExpiry: time.Hour}
	tokens := auth.NewTokenService(cfg)
	h := handlers.New(cfg, nil, nil, nil, nil, nil, nil, tokens)
	router := buildRouter(cfg, h, tokens, nil)
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/health", 200},
		{"/", 200},
		{"/missing", 404},
		{"/api/v1/me", 401},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.want {
			t.Fatalf("%s status=%d want=%d body=%s", tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestUpgradeSeedAdminHash(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	for _, tc := range []struct {
		name       string
		fake       *fakeSeedUsers
		key        []byte
		wantErr    bool
		wantUpdate bool
	}{
		{"missing", &fakeSeedUsers{getErr: repository.ErrNotFound}, key, false, false},
		{"get error", &fakeSeedUsers{getErr: errors.New("db")}, key, true, false},
		{"already protected", &fakeSeedUsers{user: &models.User{PasswordHash: protected(t, key)}}, key, false, false},
		{"upgrade", &fakeSeedUsers{user: &models.User{}}, key, false, true},
		{"bad key", &fakeSeedUsers{user: &models.User{}}, []byte("short"), true, false},
		{"update error", &fakeSeedUsers{user: &models.User{}, updateErr: errors.New("db")}, key, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := upgradeSeedAdminHash(context.Background(), tc.fake, tc.key, "sha256")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			if (tc.fake.updated != "") != tc.wantUpdate {
				t.Fatalf("updated=%t want=%t", tc.fake.updated != "", tc.wantUpdate)
			}
		})
	}
}

func protected(t *testing.T, key []byte) *string {
	t.Helper()
	value, err := auth.ProtectPassword(key, "password", "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return &value
}

func TestGetEnv(t *testing.T) {
	t.Setenv("SERVER_TEST_ENV", "set")
	if getEnv("SERVER_TEST_ENV", "fallback") != "set" {
		t.Fatal("environment value ignored")
	}
	os.Unsetenv("SERVER_TEST_ENV")
	if getEnv("SERVER_TEST_ENV", "fallback") != "fallback" {
		t.Fatal("fallback ignored")
	}
}

func TestInitialize(t *testing.T) {
	server := miniredis.RunT(t)
	newClient := func(context.Context, config.Config) (*redis.Client, error) {
		return redis.NewClient(&redis.Options{Addr: server.Addr()}), nil
	}
	makeDeps := func(t *testing.T) (startupDeps, pgxmock.PgxPoolIface) {
		t.Helper()
		pool, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		return startupDeps{
			initTracing: func(context.Context, string, string) (func(context.Context) error, error) {
				return func(context.Context) error { return nil }, errors.New("optional")
			},
			newPool:  func(context.Context, config.Config) (repository.Pool, error) { return pool, nil },
			migrate:  func(context.Context, repository.Pool, string) error { return nil },
			newRedis: newClient,
			upgrade:  func(context.Context, seedUserStore, []byte, string) error { return nil },
		}, pool
	}
	t.Run("success", func(t *testing.T) {
		deps, pool := makeDeps(t)
		pool.ExpectClose()
		app, err := initialize(context.Background(), config.Config{
			JWTSecret: "secret", JWTExpiry: time.Hour, RefreshExpiry: time.Hour,
			OTPExpiry: time.Minute,
		}, deps)
		if err != nil || app.handler == nil || app.tokens == nil || app.oauth == nil {
			t.Fatalf("app=%#v err=%v", app, err)
		}
		app.close()
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("pool error", func(t *testing.T) {
		deps, _ := makeDeps(t)
		deps.newPool = func(context.Context, config.Config) (repository.Pool, error) {
			return nil, errors.New("pool")
		}
		if _, err := initialize(context.Background(), config.Config{}, deps); err == nil {
			t.Fatal("expected pool error")
		}
	})
	t.Run("migration error", func(t *testing.T) {
		deps, pool := makeDeps(t)
		pool.ExpectClose()
		deps.migrate = func(context.Context, repository.Pool, string) error { return errors.New("migration") }
		if _, err := initialize(context.Background(), config.Config{}, deps); err == nil {
			t.Fatal("expected migration error")
		}
	})
	t.Run("redis error", func(t *testing.T) {
		deps, pool := makeDeps(t)
		pool.ExpectClose()
		deps.newRedis = func(context.Context, config.Config) (*redis.Client, error) {
			return nil, errors.New("redis")
		}
		if _, err := initialize(context.Background(), config.Config{}, deps); err == nil {
			t.Fatal("expected redis error")
		}
	})
	t.Run("upgrade error", func(t *testing.T) {
		deps, pool := makeDeps(t)
		pool.ExpectClose()
		deps.upgrade = func(context.Context, seedUserStore, []byte, string) error {
			return errors.New("upgrade")
		}
		if _, err := initialize(context.Background(), config.Config{}, deps); err == nil {
			t.Fatal("expected upgrade error")
		}
	})
	deps := defaultStartupDeps()
	if deps.initTracing == nil || deps.newPool == nil || deps.migrate == nil || deps.newRedis == nil || deps.upgrade == nil {
		t.Fatal("default dependencies incomplete")
	}
}

func TestServeLifecycleWithoutListening(t *testing.T) {
	quit := make(chan os.Signal, 1)
	quit <- os.Interrupt
	called := make(chan *http.Server, 1)
	serve(config.Config{Port: "4321"}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), quit,
		func(srv *http.Server) error {
			called <- srv
			return http.ErrServerClosed
		})
	select {
	case srv := <-called:
		if srv.Addr != ":4321" || srv.ReadHeaderTimeout != 5*time.Second || srv.IdleTimeout != time.Minute {
			t.Fatalf("server config=%#v", srv)
		}
	case <-time.After(time.Second):
		t.Fatal("listen callback was not invoked")
	}
}

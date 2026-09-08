package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tech-internal/internal/apierr"
	"tech-internal/internal/auth"
	"tech-internal/internal/config"
	"tech-internal/internal/database"
	"tech-internal/internal/fmtlog"
	"tech-internal/internal/handlers"
	"tech-internal/internal/metrics"
	"tech-internal/internal/middleware"
	"tech-internal/internal/models"
	"tech-internal/internal/otp"
	"tech-internal/internal/repository"
	"tech-internal/internal/tracing"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := config.Load()
	fmtlog.Init(cfg.ServiceName, cfg.Environment)
	ctx := context.Background()
	app, err := initialize(ctx, cfg, defaultStartupDeps())
	if err != nil {
		fmtlog.Fatal("startup_failed", map[string]any{"error.message": err.Error(), "event.action": "initialize"})
	}
	defer app.close()

	r := buildRouter(cfg, app.handler, app.tokens, app.oauth)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	serve(cfg, r, quit, func(srv *http.Server) error { return srv.ListenAndServe() })
}

func serve(cfg config.Config, handler http.Handler, quit <-chan os.Signal, listen func(*http.Server) error) {
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		fmtlog.Info("server_started", map[string]any{
			"event.action": "listen",
			"server.port":  cfg.Port,
		})
		if err := listen(srv); err != nil && err != http.ErrServerClosed {
			fmtlog.Fatal("server_failed", map[string]any{"error.message": err.Error(), "event.action": "listen"})
		}
	}()

	<-quit

	fmtlog.Info("server_stopping", map[string]any{"event.action": "shutdown"})
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmtlog.Error("shutdown_failed", map[string]any{"error.message": err.Error(), "event.action": "shutdown"})
	} else {
		fmtlog.Info("server_stopped", map[string]any{"event.action": "shutdown"})
	}
}

type startupDeps struct {
	initTracing func(context.Context, string, string) (func(context.Context) error, error)
	newPool     func(context.Context, config.Config) (repository.Pool, error)
	migrate     func(context.Context, repository.Pool, string) error
	newRedis    func(context.Context, config.Config) (*redis.Client, error)
	upgrade     func(context.Context, seedUserStore, []byte, string) error
}

type application struct {
	handler *handlers.Handler
	tokens  *auth.TokenService
	oauth   handlers.OAuthTokenStore
	close   func()
}

func defaultStartupDeps() startupDeps {
	return startupDeps{
		initTracing: tracing.Init,
		newPool: func(ctx context.Context, cfg config.Config) (repository.Pool, error) {
			return database.NewPool(ctx, cfg)
		},
		migrate: func(ctx context.Context, pool repository.Pool, dir string) error {
			return database.RunMigrations(ctx, pool, dir)
		},
		newRedis: database.NewRedis,
		upgrade:  upgradeSeedAdminHash,
	}
}

func initialize(ctx context.Context, cfg config.Config, deps startupDeps) (*application, error) {
	metrics.SetServiceName(cfg.ServiceName)
	shutdownTracing, traceErr := deps.initTracing(ctx, cfg.ServiceName, cfg.Environment)
	if traceErr != nil {
		fmtlog.Error("startup_degraded", map[string]any{"error.message": traceErr.Error(), "event.action": "tracing_init"})
	}
	pool, err := deps.newPool(ctx, cfg)
	if err != nil {
		_ = shutdownTracing(ctx)
		return nil, err
	}
	if err := deps.migrate(ctx, pool, getEnv("MIGRATION_DIR", "./migrations")); err != nil {
		pool.Close()
		_ = shutdownTracing(ctx)
		return nil, err
	}
	rdb, err := deps.newRedis(ctx, cfg)
	if err != nil {
		pool.Close()
		_ = shutdownTracing(ctx)
		return nil, err
	}
	baseUserRepo := repository.NewUserRepository(pool)
	if err := deps.upgrade(ctx, baseUserRepo, cfg.EncryptionKey, cfg.PasswordKDF); err != nil {
		_ = rdb.Close()
		pool.Close()
		_ = shutdownTracing(ctx)
		return nil, err
	}
	authCache := repository.NewAuthCache(rdb, cfg.AuthCacheSize)
	dbLimiter := repository.NewDBLimiter(cfg.MaxInflightDB)
	userRepo := repository.NewCachedUserRepository(baseUserRepo, authCache, dbLimiter)
	domainRepo := repository.NewCachedDomainRepository(
		repository.NewDomainRepository(pool), rdb, dbLimiter,
	)
	otpStore := otp.NewStore(rdb, cfg.OTPExpiry, cfg.EncryptionKey)
	appRepo := repository.NewAppRepository(pool)
	memberRepo := repository.NewMemberRepository(pool, userRepo)
	tokenRepo := repository.NewRedisTokenStore(rdb)
	tokenSvc := auth.NewTokenService(cfg)
	h := handlers.New(cfg, userRepo, domainRepo, otpStore, appRepo, memberRepo, tokenRepo, tokenSvc)
	return &application{
		handler: h, tokens: tokenSvc, oauth: tokenRepo,
		close: func() {
			_ = rdb.Close()
			pool.Close()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracing(shutdownCtx); err != nil {
				fmtlog.Error("shutdown_failed", map[string]any{"error.message": err.Error(), "event.action": "tracing_shutdown"})
			}
		},
	}, nil
}

func buildRouter(cfg config.Config, h *handlers.Handler, tokenSvc *auth.TokenService, tokenRepo middleware.AccessTokenStore) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(
		metrics.Middleware(),
		middleware.RequestID(),
		tracing.Middleware(),
		gin.Recovery(),
		middleware.AccessLog(),
		middleware.CORSMiddleware(cfg.CORSOrigins),
	)
	r.GET("/metrics", gin.WrapH(metrics.Handler()))

	api := r.Group("/api/v1")
	{
		api.GET("/health", h.Health)
		api.GET("/auth/domain", h.GetDomainSettings)
		api.POST("/auth/login/email", h.LoginEmail)
		api.POST("/auth/login/member/email", h.MemberLoginEmail)
		api.POST("/auth/login/admin/email", h.AdminLoginEmail)
		api.POST("/auth/otp/request", h.RequestOTP)
		api.POST("/auth/otp/verify", h.VerifyOTP)
		api.GET("/oauth/config", h.OAuthConfig)
		api.POST("/oauth/token", h.OAuthToken)
		api.POST("/oauth/token/member", h.MemberOAuthToken)
		api.POST("/oauth/token/admin", h.AdminOAuthToken)
		api.POST("/oauth/token/refresh", h.OAuthRefresh)

		logout := api.Group("")
		logout.Use(middleware.Authenticate(tokenSvc))
		{
			logout.POST("/auth/logout", h.Logout)
			logout.POST("/oauth/revoke", h.Logout)
		}

		protected := api.Group("")
		protected.Use(middleware.Auth(tokenSvc, tokenRepo))
		{
			protected.GET("/me", h.Me)
			protected.GET("/apps", h.ListApps)

			admin := protected.Group("/admin")
			admin.Use(middleware.AdminOnly())
			{
				admin.GET("/domain", h.AdminGetDomain)
				admin.PUT("/domain", h.AdminUpdateDomain)
				admin.GET("/users", h.AdminListUsers)
				admin.POST("/users", h.AdminRegisterUser)
				admin.PUT("/users/:type/:identifier/password", h.AdminResetPassword)
				admin.DELETE("/users/:type/:identifier", h.AdminDeleteUser)
				admin.POST("/apps", h.AdminCreateApp)
				admin.DELETE("/apps/:id", h.AdminDeleteApp)
			}
		}
	}

	v2 := r.Group("/api/v2")
	v2.Use(middleware.Auth(tokenSvc, tokenRepo))
	{
		orgs := v2.Group("/organizations/:id")
		{
			members := orgs.Group("/members")
			members.Use(middleware.RequirePermission("members:read"))
			{
				members.GET("", h.ListMembers)
				members.POST("", middleware.RequirePermission("members:write"), h.CreateMember)
				members.PATCH("/:member_id", middleware.RequirePermission("members:write"), h.UpdateMember)
				members.DELETE("/:member_id", middleware.RequirePermission("members:write"), h.DeleteMember)
			}
			roles := orgs.Group("/roles")
			roles.Use(middleware.RequirePermission("roles:read"))
			{
				roles.GET("", h.ListRoles)
				roles.POST("", middleware.RequirePermission("roles:write"), h.CreateRole)
			}
		}
	}

	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service": "tech-internal-api",
			"docs":    "/api/v1/health",
		})
	})
	r.NoRoute(func(c *gin.Context) {
		apierr.JSON(c, http.StatusNotFound, apierr.RouteNotFound, "not found")
	})
	return r
}

type seedUserStore interface {
	Get(context.Context, string, string, string) (*models.User, error)
	UpdatePasswordHash(context.Context, string, string, string, string) error
}

func upgradeSeedAdminHash(
	ctx context.Context,
	users seedUserStore,
	encryptionKey []byte,
	passwordKDF string,
) error {
	const (
		domain     = "techhr.com"
		identifier = "admin@techhr.com"
		password   = "Admin@123"
	)

	user, err := users.Get(ctx, domain, "email", identifier)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return err
	}
	if user.PasswordHash != nil && auth.IsProtected(*user.PasswordHash) {
		return nil
	}

	hash, err := auth.ProtectPassword(encryptionKey, password, passwordKDF)
	if err != nil {
		return err
	}
	return users.UpdatePasswordHash(ctx, domain, "email", identifier, hash)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

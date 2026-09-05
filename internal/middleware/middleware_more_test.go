package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tech-internal/internal/auth"
	"tech-internal/internal/config"
	"tech-internal/internal/models"
	"tech-internal/internal/repository"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

func runMiddleware(t *testing.T, middleware gin.HandlerFunc, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.GET("/", middleware, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAuthenticationErrorsAndAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokens := auth.NewTokenService(config.Config{JWTSecret: "secret", JWTExpiry: time.Hour, RefreshExpiry: time.Hour})
	for name, header := range map[string]string{"missing": "", "malformed": "Basic abc", "invalid": "Bearer no"} {
		t.Run(name, func(t *testing.T) {
			rec := runMiddleware(t, Authenticate(tokens), map[string]string{"Authorization": header})
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	user := &models.User{MembershipID: "m", Domain: "d", Type: "email", Identifier: "i"}
	pair, _ := tokens.IssuePair(user, "")
	if rec := runMiddleware(t, Authenticate(tokens), map[string]string{"Authorization": "Bearer " + pair.AccessToken}); rec.Code != http.StatusNoContent {
		t.Fatalf("valid status=%d", rec.Code)
	}

	for _, tc := range []struct {
		name string
		mw   gin.HandlerFunc
		set  any
		want int
	}{
		{"admin missing", AdminOnly(), nil, 401},
		{"admin wrong", AdminOnly(), "wrong", 403},
		{"admin role", AdminOnly(), &models.Claims{Roles: []string{"ORG_ADMIN"}}, 204},
		{"admin legacy", AdminOnly(), &models.Claims{Role: models.RoleAdmin}, 204},
		{"admin permission", AdminOnly(), &models.Claims{Permissions: []string{"members:write"}}, 204},
		{"permission missing", RequirePermission("x"), nil, 401},
		{"permission denied", RequirePermission("x"), &models.Claims{}, 403},
		{"permission direct", RequirePermission("x"), &models.Claims{Permissions: []string{"x"}}, 204},
		{"permission wildcard", RequirePermission("x"), &models.Claims{Scopes: []string{"*:*"}}, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/", func(c *gin.Context) {
				if tc.set != nil {
					c.Set("claims", tc.set)
				}
			}, tc.mw, func(c *gin.Context) { c.Status(204) })
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d", rec.Code, tc.want)
			}
		})
	}
	if hasRole(nil, "x") || hasPermission(nil, "x") {
		t.Fatal("nil claims authorized")
	}
}

func TestAuthStoreFailuresAndMismatch(t *testing.T) {
	tokens := auth.NewTokenService(config.Config{JWTSecret: "secret", JWTExpiry: time.Hour, RefreshExpiry: time.Hour})
	user := &models.User{MembershipID: "m", OrganizationID: "o", IdentityID: "i", Domain: "d", Type: "email", Identifier: "u"}
	pair, _ := tokens.IssuePair(user, "f")
	header := map[string]string{"Authorization": "Bearer " + pair.AccessToken}
	for name, store := range map[string]*jtiStore{
		"server":   {err: errors.New("db")},
		"notfound": {err: repository.ErrNotFound},
		"mismatch": {rec: &models.OAuthToken{Kind: models.TokenKindRefresh}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := runMiddleware(t, Auth(tokens, store), header)
			if rec.Code != map[string]int{"server": 500, "notfound": 401, "mismatch": 401}[name] {
				t.Fatalf("status=%d", rec.Code)
			}
		})
	}
	for name, headerValue := range map[string]string{"missing": "", "malformed": "Basic x", "invalid": "Bearer bad"} {
		t.Run(name, func(t *testing.T) {
			rec := runMiddleware(t, Auth(tokens, &jtiStore{}), map[string]string{"Authorization": headerValue})
			if rec.Code != 401 {
				t.Fatalf("status=%d", rec.Code)
			}
		})
	}
}

func TestCORSRequestIDAccessLogAndTraceIDs(t *testing.T) {
	for _, tc := range []struct {
		name, method, origin string
		allowed              []string
		want                 int
		header               string
	}{
		{"allow all", "GET", "https://a.test/", nil, 204, "https://a.test"},
		{"explicit", "GET", "HTTPS://A.TEST", []string{"https://a.test"}, 204, "HTTPS://A.TEST"},
		{"denied", "GET", "https://b.test", []string{"https://a.test"}, 204, ""},
		{"preflight", "OPTIONS", "", []string{"*"}, 204, "*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(CORSMiddleware(tc.allowed))
			r.Handle(tc.method, "/", func(c *gin.Context) { c.Status(204) })
			req := httptest.NewRequest(tc.method, "/", nil)
			req.Header.Set("Origin", tc.origin)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.want || rec.Header().Get("Access-Control-Allow-Origin") != tc.header {
				t.Fatalf("status=%d origin=%q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
			}
		})
	}

	r := gin.New()
	r.Use(RequestID(), AccessLog())
	r.GET("/", func(c *gin.Context) {
		c.Set("claims", &models.Claims{Domain: "d", Identifier: "i", Role: "r"})
		c.Status(500)
	})
	req := httptest.NewRequest("GET", "/?a=b", nil)
	req.Header.Set(RequestIDHeader, " supplied ")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Header().Get(RequestIDHeader) != "supplied" {
		t.Fatalf("request id=%q", rec.Header().Get(RequestIDHeader))
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(RequestIDKey, "fallback")
	if TraceID(c) != "fallback" || SpanID(c) != "" {
		t.Fatal("fallback IDs failed")
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	})
	c.Request = c.Request.WithContext(trace.ContextWithSpanContext(context.Background(), sc))
	if TraceID(c) != sc.TraceID().String() || SpanID(c) != sc.SpanID().String() {
		t.Fatal("span IDs failed")
	}
}

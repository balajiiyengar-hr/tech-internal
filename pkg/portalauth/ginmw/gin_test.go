package ginmw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tech-internal/pkg/portalauth"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func token(t *testing.T, secret []byte) string {
	t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "member", "domain": "acme", "aud": portalauth.Audience,
		"token_use": portalauth.TokenUseAccess, "exp": time.Now().Add(time.Hour).Unix(),
		"role": "admin", "roles": []string{"admin"},
	}).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGinMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("secret")
	r := gin.New()
	r.GET("/", Authenticate(secret), Authorize(portalauth.RequireRole("admin")), func(c *gin.Context) {
		if claims, ok := ClaimsFromGin(c); !ok || claims.UserID != "member" {
			t.Fatalf("claims=%#v ok=%v", claims, ok)
		}
		if _, ok := portalauth.FromContext(c.Request.Context()); !ok {
			t.Fatal("claims absent from request context")
		}
		c.Status(204)
	})
	for name, header := range map[string]string{
		"valid": "Bearer " + token(t, secret), "missing": "", "invalid": "Bearer bad",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", header)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			want := 204
			if name != "valid" {
				want = 401
			}
			if rec.Code != want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuthorizeAndClaimsFallback(t *testing.T) {
	r := gin.New()
	r.GET("/", Authorize(portalauth.RequireRole("admin")), func(c *gin.Context) { c.Status(204) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("missing status=%d", rec.Code)
	}

	claims := &portalauth.Claims{Roles: []string{"member"}}
	r = gin.New()
	r.GET("/", func(c *gin.Context) {
		c.Request = c.Request.WithContext(portalauth.WithClaims(context.Background(), claims))
	}, Authorize(portalauth.RequireRole("admin")), func(c *gin.Context) { c.Status(204) })
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forbidden status=%d", rec.Code)
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(ClaimsKey, "wrong")
	if _, ok := ClaimsFromGin(c); ok {
		t.Fatal("wrong claim type accepted")
	}
}

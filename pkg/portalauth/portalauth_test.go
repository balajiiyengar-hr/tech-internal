package portalauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func signed(t *testing.T, secret []byte, use string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "member", "domain": "acme.test", "type": "email",
		"identifier": "person@acme.test", "role": "admin",
		"roles": []string{"admin"}, "permissions": []string{"members:read"},
		"scopes": []string{"apps:*"}, "aud": Audience, "jti": "jti-1",
		"token_use": use, "family_id": "family-1",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	})
	raw, err := token.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAuthenticationAndBearer(t *testing.T) {
	secret := []byte("secret")
	access := signed(t, secret, TokenUseAccess)
	claims, err := Authenticate(secret, access)
	if err != nil || claims.JTI != "jti-1" || claims.UserID != "member" {
		t.Fatalf("Authenticate=%+v,%v", claims, err)
	}
	tests := []struct {
		header string
		want   error
	}{
		{"Bearer " + access, nil},
		{"bearer " + access, nil},
		{"", ErrMissingToken},
		{"Basic abc", ErrInvalidToken},
		{"Bearer ", ErrInvalidToken},
	}
	for _, tt := range tests {
		raw, err := BearerToken(tt.header)
		if !errors.Is(err, tt.want) || (tt.want == nil && raw != access) {
			t.Errorf("BearerToken(%q)=%q,%v", tt.header, raw, err)
		}
	}
	if _, err := Authenticate(secret, signed(t, secret, TokenUseRefresh)); !errors.Is(err, ErrWrongUse) {
		t.Fatalf("refresh authenticate error=%v", err)
	}
	if _, err := ParseSigned(nil, access); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("empty secret error=%v", err)
	}
}

func TestClaimsAndAuthorization(t *testing.T) {
	claims := &Claims{
		UserID: "member", Domain: "acme.test", Type: "email",
		Identifier: "person@acme.test", Role: RoleAdmin,
		Scopes: []string{"apps:*", "profile:read"},
	}
	allowed := []Rule{
		RequireRole("admin"), RequireScope("apps:read", "profile:read"),
		RequireAnyScope("missing:x", "apps:write"), RequireResource("apps", "read"),
		RequireUserID("member"), RequireDomain("ACME.TEST"), RequireAudience(Audience),
	}
	if err := Authorize(claims, allowed...); err != nil {
		t.Fatal(err)
	}
	for _, rule := range []Rule{
		RequireRole("user"), RequireScope("users:write"), RequireAnyScope("none:x"),
		RequireUserID("other"), RequireDomain("other.test"),
	} {
		if err := Authorize(claims, rule); !errors.Is(err, ErrForbidden) {
			t.Errorf("expected forbidden, got %v", err)
		}
	}
	if !claims.CanAccess("apps", "write") || claims.CanAccess("", "read") {
		t.Fatal("scope matching result incorrect")
	}
	if UserID(" ACME.TEST ", "EMAIL", " person@example.com ") != "acme.test/email/person@example.com" {
		t.Fatal("UserID normalization incorrect")
	}
}

func TestClaimsEdgeCases(t *testing.T) {
	if (*Claims)(nil).EffectiveScopes() != nil || (*Claims)(nil).HasRole("admin") || (*Claims)(nil).HasScope("x:y") {
		t.Fatal("nil claims authorized")
	}
	admin := &Claims{Role: " ADMIN "}
	if got := admin.EffectiveScopes(); len(got) != 7 {
		t.Fatalf("admin defaults=%v", got)
	}
	member := &Claims{Role: "member"}
	if got := member.EffectiveScopes(); len(got) != 2 {
		t.Fatalf("member defaults=%v", got)
	}
	permissions := &Claims{Permissions: []string{"users:read"}}
	if got := permissions.EffectiveScopes(); len(got) != 1 || got[0] != "users:read" {
		t.Fatalf("permissions=%v", got)
	}
	if !(&Claims{Role: "ADMIN"}).HasRole("admin") {
		t.Fatal("case-insensitive role failed")
	}
	for _, tc := range []struct {
		have, want string
		ok         bool
	}{
		{"*", "x:y", true}, {"*:*", "x:y", true}, {" X:Y ", "x:y", true},
		{"x:*", "x:y", true}, {"*:y", "x:y", true}, {"x:z", "x:y", false},
		{"", "x:y", false}, {"bad", "x:y", false}, {"x:y", "bad", false},
	} {
		if got := scopeCovers(tc.have, tc.want); got != tc.ok {
			t.Errorf("scopeCovers(%q,%q)=%v", tc.have, tc.want, got)
		}
	}
	if (&Claims{Scopes: []string{"x:y"}}).CanAccess("", "y") ||
		(&Claims{Scopes: []string{"x:y"}}).CanAccess("x", "") {
		t.Fatal("empty resource/action accepted")
	}
}

func TestProtectHTTPMiddleware(t *testing.T) {
	secret := []byte("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claims, ok := FromContext(r.Context()); !ok || claims.Domain != "acme.test" {
			t.Fatal("claims missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := Protect(secret, next, RequireScope("apps:read"))
	tests := []struct {
		name, token string
		want        int
	}{
		{"allowed", signed(t, secret, TokenUseAccess), http.StatusNoContent},
		{"missing", "", http.StatusUnauthorized},
		{"wrong signature", signed(t, []byte("other"), TokenUseAccess), http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.token != "" {
				req.Header.Set(HeaderAuthorization, "Bearer "+tt.token)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestHTTPAdaptersEdgeCases(t *testing.T) {
	claims := &Claims{Role: "member"}
	ctx := WithClaims(context.Background(), claims)
	if got, ok := FromContext(ctx); !ok || got != claims {
		t.Fatal("WithClaims round trip failed")
	}
	if got, ok := FromContext(WithClaims(context.Background(), nil)); ok || got != nil {
		t.Fatal("nil claims accepted")
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	rec := httptest.NewRecorder()
	AuthorizeMiddleware(RequireRole("admin"))(next).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("missing claims=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil).WithContext(WithClaims(context.Background(), claims))
	AuthorizeMiddleware(RequireRole("admin"))(next).ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("forbidden=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil).WithContext(WithClaims(context.Background(), &Claims{Role: "admin"}))
	AuthorizeMiddleware(RequireRole("admin"))(next).ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("allowed=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	Protect([]byte("secret"), next).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("protect no rules=%d", rec.Code)
	}
}

func TestClaimConversionAndTokenErrors(t *testing.T) {
	m := jwt.MapClaims{
		"sub": 123, "roles": []string{"admin"}, "permissions": []any{" read ", "", 2},
		"scopes": "apps:read users:read", "exp": float64(10), "iat": json.Number("20"),
	}
	claims := claimsFromMap(m)
	if claims.UserID != "123" || len(claims.Roles) != 1 || len(claims.Permissions) != 2 ||
		len(claims.Scopes) != 2 || claims.ExpiresAt.Unix() != 10 || claims.IssuedAt.Unix() != 20 {
		t.Fatalf("claims=%#v", claims)
	}
	if claimString(nil, "x") != "" || claimStringSlice(nil, "x") != nil {
		t.Fatal("missing claims conversion failed")
	}
	if claimStringSlice(jwt.MapClaims{"x": 123}, "x") != nil ||
		claimStringSlice(jwt.MapClaims{"x": ""}, "x") != nil {
		t.Fatal("invalid slice conversion accepted")
	}
	for _, tc := range []struct {
		value any
		ok    bool
	}{
		{int64(4), true}, {json.Number("5"), true}, {json.Number("x"), false},
		{nil, false}, {"5", false},
	} {
		got, ok := claimUnix(jwt.MapClaims{"x": tc.value}, "x")
		if ok != tc.ok || (ok && got == 0) {
			t.Errorf("claimUnix(%v)=%d,%v", tc.value, got, ok)
		}
	}
	if _, ok := claimUnix(nil, "x"); ok {
		t.Fatal("missing unix claim accepted")
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": time.Now().Add(-time.Hour).Unix(),
	}).SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSigned([]byte("secret"), expired); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expired=%v", err)
	}
	if _, err := ParseSigned([]byte("secret"), ""); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("empty=%v", err)
	}
	if _, err := ParseSigned([]byte("secret"), "not.jwt"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid=%v", err)
	}
}

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"tech-internal/internal/auth"
	"tech-internal/internal/config"
	"tech-internal/internal/models"
	"tech-internal/internal/otp"
	"tech-internal/internal/repository"

	"github.com/gin-gonic/gin"
)

type p0Users map[string]*models.User

func (f p0Users) Get(_ context.Context, domain, typ, identifier string) (*models.User, error) {
	u, ok := f[domain+"|"+typ+"|"+identifier]
	if !ok {
		return nil, repository.ErrNotFound
	}
	copy := *u
	return &copy, nil
}
func (p0Users) ListByDomain(context.Context, string, int, int) ([]models.User, int64, error) {
	return nil, 0, nil
}
func (p0Users) Create(context.Context, models.User, *string) error { return nil }
func (p0Users) UpdatePasswordHash(context.Context, string, string, string, string) error {
	return nil
}

type p0Domains map[string]models.DomainSettings

func (f p0Domains) Get(_ context.Context, domain string) (*models.DomainSettings, error) {
	d, ok := f[domain]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &d, nil
}
func (p0Domains) Update(context.Context, models.DomainSettings) error { return nil }

type p0OTPs struct {
	key, protected []byte
	domain, id     string
}

func (f *p0OTPs) Save(_ context.Context, domain, id, protected string) error {
	f.domain, f.id, f.protected = domain, id, []byte(protected)
	return nil
}
func (f *p0OTPs) VerifyAndConsume(_ context.Context, domain, id, code string) error {
	if domain != f.domain || id != f.id || len(f.protected) == 0 {
		return otp.ErrExpired
	}
	want, err := auth.OpenOTP(f.key, string(f.protected))
	if err != nil || want != code {
		return otp.ErrMismatch
	}
	f.protected = nil
	return nil
}

type p0OAuth struct {
	mu sync.Mutex
	m  map[string]models.OAuthToken
}

func (f *p0OAuth) InsertPair(_ context.Context, a models.OAuthToken, _ string, r models.OAuthToken, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[a.JTI], f.m[r.JTI] = a, r
	return nil
}
func (f *p0OAuth) RotatePair(_ context.Context, old string, a models.OAuthToken, _ string, r models.OAuthToken, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[old]; !ok {
		return false, nil
	}
	delete(f.m, old)
	f.m[a.JTI], f.m[r.JTI] = a, r
	return true, nil
}
func (f *p0OAuth) GetActiveByJTI(_ context.Context, jti string) (*models.OAuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.m[jti]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &rec, nil
}
func (f *p0OAuth) RevokeFamily(_ context.Context, family string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for jti, rec := range f.m {
		if rec.FamilyID == family {
			delete(f.m, jti)
			count++
		}
	}
	return count, nil
}

func newP0Handler(t *testing.T) (*Handler, *p0OTPs) {
	t.Helper()
	key := []byte("0123456789abcdef0123456789abcdef")
	hash, err := auth.ProtectPassword(key, "correct-password", "sha256")
	if err != nil {
		t.Fatal(err)
	}
	base := models.User{
		PersonID: "person", MembershipID: "member", IdentityID: "identity",
		OrganizationID: "org", Domain: "acme.test", Type: models.UserTypeEmail,
		Identifier: "person@acme.test", PasswordHash: &hash, Active: true,
		Roles: []string{"member"},
	}
	sms := base
	sms.Type, sms.Identifier, sms.PasswordHash = models.UserTypeSMS, "+15550001111", nil
	users := p0Users{
		"acme.test|email|person@acme.test": &base,
		"acme.test|sms|+15550001111":       &sms,
	}
	domains := p0Domains{"acme.test": {
		Domain: "acme.test", EmailLoginEnabled: true, SMSLoginEnabled: true,
	}}
	codes := &p0OTPs{key: key}
	store := &p0OAuth{m: map[string]models.OAuthToken{}}
	cfg := config.Config{
		EncryptionKey: key, PasswordKDF: "sha256", DevLogOTP: true,
		OTPExpiry: 2 * time.Minute, JWTSecret: "p0-secret",
		JWTExpiry: time.Hour, RefreshExpiry: 24 * time.Hour,
	}
	return New(cfg, users, domains, codes, nil, nil, store, auth.NewTokenService(cfg)), codes
}

func postJSON(handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return rec
}

func TestEmailLoginP0(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newP0Handler(t)
	tests := []struct {
		name, body string
		want       int
	}{
		{"success", `{"organization":"acme.test","identifier":"PERSON@ACME.TEST","password":"correct-password","portal":"member"}`, 200},
		{"bad password", `{"organization":"acme.test","identifier":"person@acme.test","password":"wrong"}`, 401},
		{"wrong organization", `{"organization":"other.test","identifier":"person@acme.test","password":"correct-password"}`, 401},
		{"wrong portal", `{"organization":"acme.test","identifier":"person@acme.test","password":"correct-password","portal":"admin"}`, 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := postJSON(h.LoginEmail, tt.body); got.Code != tt.want {
				t.Fatalf("status=%d want=%d body=%s", got.Code, tt.want, got.Body.String())
			}
		})
	}
}

func TestOTPRequestVerifyDEVLOGOTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newP0Handler(t)
	request := postJSON(h.RequestOTP, `{"organization":"acme.test","identifier":"+15550001111","portal":"member"}`)
	if request.Code != 200 {
		t.Fatalf("request: %d %s", request.Code, request.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(request.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	code, ok := body["dev_code"].(string)
	if !ok || len(code) != 6 {
		t.Fatalf("missing DEV_LOG_OTP code: %s", request.Body.String())
	}
	verify := postJSON(h.VerifyOTP, `{"organization":"acme.test","identifier":"+15550001111","portal":"member","otp":"`+code+`"}`)
	if verify.Code != 200 {
		t.Fatalf("verify: %d %s", verify.Code, verify.Body.String())
	}
}

func TestIssueRefreshAndHealthP0(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newP0Handler(t)
	issued := postJSON(h.OAuthToken, `{"organization":"acme.test","identifier":"person@acme.test","password":"correct-password","portal":"member"}`)
	var pair map[string]any
	if issued.Code != 200 || json.Unmarshal(issued.Body.Bytes(), &pair) != nil {
		t.Fatalf("issue: %d %s", issued.Code, issued.Body.String())
	}
	refreshed := postJSON(h.OAuthRefresh, `{"refresh_token":"`+pair["refresh_token"].(string)+`"}`)
	if refreshed.Code != 200 {
		t.Fatalf("refresh: %d %s", refreshed.Code, refreshed.Body.String())
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	h.Health(c)
	if rec.Code != 200 {
		t.Fatalf("health=%d", rec.Code)
	}
}

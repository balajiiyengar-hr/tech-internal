package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tech-internal/internal/auth"
	"tech-internal/internal/config"
	"tech-internal/internal/models"
	"tech-internal/internal/otp"
	"tech-internal/internal/repository"

	"github.com/gin-gonic/gin"
)

type fakeStores struct {
	user        *models.User
	users       []models.User
	domain      *models.DomainSettings
	apps        []models.PortalApp
	members     []models.Member
	roles       []models.OrganizationRole
	err         error
	getMember   *models.Member
	createdRole *models.OrganizationRole
	createErr   error
	updateErr   error
}

func (f *fakeStores) Get(context.Context, string, string, string) (*models.User, error) {
	return f.user, f.err
}
func (f *fakeStores) ListByDomain(context.Context, string, int, int) ([]models.User, int64, error) {
	return f.users, int64(len(f.users)), f.err
}
func (f *fakeStores) Create(context.Context, models.User, *string) error {
	if f.createErr != nil {
		return f.createErr
	}
	return f.err
}
func (f *fakeStores) UpdatePasswordHash(context.Context, string, string, string, string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	return f.err
}

type fakeDomain struct{ data *models.DomainSettings; err error }

func (f *fakeDomain) Get(context.Context, string) (*models.DomainSettings, error) { return f.data, f.err }
func (f *fakeDomain) Update(context.Context, models.DomainSettings) error         { return f.err }

type fakeApps struct{ list []models.PortalApp; err error }

func (f *fakeApps) ListByDomain(context.Context, string, int, int) ([]models.PortalApp, int64, error) {
	return f.list, int64(len(f.list)), f.err
}
func (f *fakeApps) Create(_ context.Context, app *models.PortalApp) error {
	app.ID = "new"
	return f.err
}
func (f *fakeApps) Delete(context.Context, string) error { return f.err }

type fakeMembers struct {
	list []models.Member
	get  *models.Member
	roles []models.OrganizationRole
	role *models.OrganizationRole
	err  error
}

type fakeOTP struct{ err error }

func (f *fakeOTP) Save(context.Context, string, string, string) error { return f.err }
func (f *fakeOTP) VerifyAndConsume(context.Context, string, string, string) error {
	return f.err
}

type fakeOAuth struct{ err error }

func (f *fakeOAuth) InsertPair(context.Context, models.OAuthToken, string, models.OAuthToken, string) error {
	return f.err
}
func (f *fakeOAuth) RotatePair(context.Context, string, models.OAuthToken, string, models.OAuthToken, string) (bool, error) {
	return false, f.err
}
func (f *fakeOAuth) GetActiveByJTI(context.Context, string) (*models.OAuthToken, error) {
	return nil, f.err
}
func (f *fakeOAuth) RevokeFamily(context.Context, string) (int64, error) { return 0, f.err }

func (f *fakeMembers) DeleteByIdentity(context.Context, string, string, string, string) error {
	return f.err
}
func (f *fakeMembers) List(context.Context, string, int, int) ([]models.Member, int64, error) {
	return f.list, int64(len(f.list)), f.err
}
func (f *fakeMembers) Get(context.Context, string, string) (*models.Member, error) {
	return f.get, f.err
}
func (f *fakeMembers) Create(_ context.Context, _ string, m *models.Member) error {
	m.ID = "new"
	return f.err
}
func (f *fakeMembers) Update(context.Context, string, string, string, *bool, []string) error {
	return f.err
}
func (f *fakeMembers) Delete(context.Context, string, string) error { return f.err }
func (f *fakeMembers) ListRoles(context.Context, string, int, int) ([]models.OrganizationRole, int64, error) {
	return f.roles, int64(len(f.roles)), f.err
}
func (f *fakeMembers) CreateRole(context.Context, string, string, string, []string) (*models.OrganizationRole, error) {
	return f.role, f.err
}

func handlerRequest(t *testing.T, h gin.HandlerFunc, method, path, body string, claims *models.Claims, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	if claims != nil {
		c.Set("claims", claims)
	}
	h(c)
	return rec
}

func TestReadAndDomainHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &models.Claims{Domain: "acme", OrganizationID: "org", UserID: "me"}
	for _, tc := range []struct {
		name string
		call func(*Handler) gin.HandlerFunc
		err  error
		want int
	}{
		{"get domain", func(h *Handler) gin.HandlerFunc { return h.GetDomainSettings }, nil, 200},
		{"get domain missing", func(h *Handler) gin.HandlerFunc { return h.GetDomainSettings }, repository.ErrNotFound, 404},
		{"get domain error", func(h *Handler) gin.HandlerFunc { return h.GetDomainSettings }, errors.New("db"), 500},
		{"me", func(h *Handler) gin.HandlerFunc { return h.Me }, nil, 200},
		{"me error", func(h *Handler) gin.HandlerFunc { return h.Me }, errors.New("db"), 500},
		{"admin domain", func(h *Handler) gin.HandlerFunc { return h.AdminGetDomain }, nil, 200},
		{"apps", func(h *Handler) gin.HandlerFunc { return h.ListApps }, nil, 200},
		{"users", func(h *Handler) gin.HandlerFunc { return h.AdminListUsers }, nil, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			users := &fakeStores{users: []models.User{{Domain: "acme"}}, err: tc.err}
			domains := &fakeDomain{data: &models.DomainSettings{Domain: "acme", EmailLoginEnabled: true}, err: tc.err}
			apps := &fakeApps{list: []models.PortalApp{{ID: "a"}}, err: tc.err}
			h := &Handler{users: users, domains: domains, apps: apps}
			path := "/?domain=acme"
			rec := handlerRequest(t, tc.call(h), "GET", path, "", claims, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	h := &Handler{}
	if rec := handlerRequest(t, h.GetDomainSettings, "GET", "/", "", nil, nil); rec.Code != 400 {
		t.Fatalf("missing domain status=%d", rec.Code)
	}
}

func TestAdminDomainAndAppMutations(t *testing.T) {
	claims := &models.Claims{Domain: "acme"}
	for _, tc := range []struct {
		name, body string
		err        error
		want       int
	}{
		{"update", `{"email_login_enabled":true,"sms_login_enabled":false}`, nil, 200},
		{"invalid", `{`, nil, 400},
		{"disable all", `{"email_login_enabled":false,"sms_login_enabled":false}`, nil, 400},
		{"load error", `{}`, errors.New("db"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{domains: &fakeDomain{data: &models.DomainSettings{Domain: "acme", EmailLoginEnabled: true}, err: tc.err}}
			rec := handlerRequest(t, h.AdminUpdateDomain, "PUT", "/", tc.body, claims, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		name, body string
		err        error
		want       int
	}{
		{"create app", `{"name":"A","url":"https://a","section":""}`, nil, 201},
		{"bad app", `{}`, nil, 400},
		{"create error", `{"name":"A","url":"https://a"}`, errors.New("db"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{apps: &fakeApps{err: tc.err}}
			rec := handlerRequest(t, h.AdminCreateApp, "POST", "/", tc.body, claims, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	for _, tc := range []struct{ err error; want int }{{nil, 200}, {repository.ErrNotFound, 404}, {errors.New("db"), 500}} {
		h := &Handler{apps: &fakeApps{err: tc.err}}
		rec := handlerRequest(t, h.AdminDeleteApp, "DELETE", "/", "", claims, gin.Params{{Key: "id", Value: "a"}})
		if rec.Code != tc.want {
			t.Fatalf("delete status=%d want=%d", rec.Code, tc.want)
		}
	}
}

func TestMemberAndRoleHandlers(t *testing.T) {
	claims := &models.Claims{Domain: "acme", OrganizationID: "org", UserID: "caller"}
	params := gin.Params{{Key: "id", Value: "org"}, {Key: "member_id", Value: "target"}}
	t.Run("lists", func(t *testing.T) {
		store := &fakeMembers{list: []models.Member{{ID: "m"}}, roles: []models.OrganizationRole{{ID: "r"}}}
		h := &Handler{members: store}
		for _, call := range []gin.HandlerFunc{h.ListMembers, h.ListRoles} {
			if rec := handlerRequest(t, call, "GET", "/", "", claims, params); rec.Code != 200 {
				t.Fatalf("list status=%d", rec.Code)
			}
		}
		store.err = errors.New("db")
		for _, call := range []gin.HandlerFunc{h.ListMembers, h.ListRoles} {
			if rec := handlerRequest(t, call, "GET", "/", "", claims, params); rec.Code != 500 {
				t.Fatalf("list error status=%d", rec.Code)
			}
		}
		bad := gin.Params{{Key: "id", Value: "other"}}
		if rec := handlerRequest(t, h.ListMembers, "GET", "/", "", claims, bad); rec.Code != 403 {
			t.Fatalf("cross org=%d", rec.Code)
		}
	})

	for _, tc := range []struct {
		name, body string
		err        error
		want       int
	}{
		{"create", `{"display_name":"A","role_keys":["member"],"phone":"123"}`, nil, 201},
		{"invalid", `{}`, nil, 400},
		{"no identity", `{"display_name":"A","role_keys":["member"]}`, nil, 400},
		{"short password", `{"display_name":"A","role_keys":["member"],"email":"a@b","password":"x"}`, nil, 400},
		{"conflict", `{"display_name":"A","role_keys":["member"],"phone":"123"}`, repository.ErrConflict, 409},
		{"role", `{"display_name":"A","role_keys":["member"],"phone":"123"}`, errors.New("unknown role x"), 400},
		{"error", `{"display_name":"A","role_keys":["member"],"phone":"123"}`, errors.New("db"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeMembers{get: &models.Member{ID: "new"}, err: tc.err}
			h := &Handler{members: store, cfg: config.Config{EncryptionKey: []byte("0123456789abcdef0123456789abcdef"), PasswordKDF: "sha256"}}
			rec := handlerRequest(t, h.CreateMember, "POST", "/", tc.body, claims, params)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"update", nil, 200}, {"update missing", repository.ErrNotFound, 404},
		{"update last", repository.ErrLastAdmin, 409}, {"update role", errors.New("unknown role"), 400},
		{"update error", errors.New("db"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{members: &fakeMembers{get: &models.Member{ID: "target"}, err: tc.err}}
			rec := handlerRequest(t, h.UpdateMember, "PATCH", "/", `{"display_name":"A"}`, claims, params)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	for _, tc := range []struct{ err error; want int }{{nil, 200}, {repository.ErrNotFound, 404}, {repository.ErrLastAdmin, 409}, {errors.New("db"), 500}} {
		h := &Handler{members: &fakeMembers{err: tc.err}}
		rec := handlerRequest(t, h.DeleteMember, "DELETE", "/", "", claims, params)
		if rec.Code != tc.want {
			t.Fatalf("delete status=%d want=%d", rec.Code, tc.want)
		}
	}
	self := gin.Params{{Key: "id", Value: "org"}, {Key: "member_id", Value: "caller"}}
	if rec := handlerRequest(t, (&Handler{}).DeleteMember, "DELETE", "/", "", claims, self); rec.Code != 409 {
		t.Fatalf("self delete=%d", rec.Code)
	}

	for _, tc := range []struct {
		body string
		err  error
		want int
	}{
		{`{"key":"Custom Role","name":"Custom"}`, nil, 201},
		{`{}`, nil, 400},
		{`{"key":"x","name":"X"}`, repository.ErrConflict, 409},
		{`{"key":"x","name":"X"}`, errors.New("unknown permission"), 400},
		{`{"key":"x","name":"X"}`, errors.New("db"), 500},
	} {
		h := &Handler{members: &fakeMembers{role: &models.OrganizationRole{ID: "r"}, err: tc.err}}
		rec := handlerRequest(t, h.CreateRole, "POST", "/", tc.body, claims, params)
		if rec.Code != tc.want {
			t.Fatalf("role status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestSimpleHelpers(t *testing.T) {
	h := &Handler{}
	if rec := handlerRequest(t, h.Health, "GET", "/", "", nil, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if loginOrganization(" Domain ", " Org ") != "org" || loginOrganization(" Domain ", "") != "domain" {
		t.Fatal("organization normalization")
	}
	if got := paginationResponse(10, 1, 11); got["has_next"] != true {
		t.Fatalf("pagination=%#v", got)
	}
}

func TestAdminRegistrationAndPasswordReset(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	claims := &models.Claims{Domain: "acme"}
	for _, tc := range []struct {
		name, body string
		domain     *models.DomainSettings
		err        error
		want       int
	}{
		{"email", `{"domain":"acme","type":"email","identifier":"A@B","password":"password1"}`, &models.DomainSettings{EmailLoginEnabled: true}, nil, 201},
		{"sms", `{"domain":"acme","type":"sms","identifier":"123"}`, &models.DomainSettings{SMSLoginEnabled: true}, nil, 201},
		{"invalid", `{}`, &models.DomainSettings{}, nil, 400},
		{"cross domain", `{"domain":"other","type":"email","identifier":"a","password":"password1"}`, &models.DomainSettings{EmailLoginEnabled: true}, nil, 403},
		{"bad type", `{"domain":"acme","type":"other","identifier":"a"}`, &models.DomainSettings{}, nil, 400},
		{"domain error", `{"domain":"acme","type":"email","identifier":"a","password":"password1"}`, nil, errors.New("db"), 500},
		{"email disabled", `{"domain":"acme","type":"email","identifier":"a","password":"password1"}`, &models.DomainSettings{}, nil, 400},
		{"sms disabled", `{"domain":"acme","type":"sms","identifier":"1"}`, &models.DomainSettings{}, nil, 400},
		{"password missing", `{"domain":"acme","type":"email","identifier":"a"}`, &models.DomainSettings{EmailLoginEnabled: true}, nil, 400},
		{"create error", `{"domain":"acme","type":"email","identifier":"a","password":"password1"}`, &models.DomainSettings{EmailLoginEnabled: true}, errors.New("db"), 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			users := &fakeStores{err: tc.err}
			domainErr := tc.err
			if tc.name == "create error" {
				domainErr = nil
				users.err = nil
				users.createErr = tc.err
			}
			h := &Handler{
				cfg: config.Config{EncryptionKey: key, PasswordKDF: "sha256"},
				users: users, domains: &fakeDomain{data: tc.domain, err: domainErr},
			}
			rec := handlerRequest(t, h.AdminRegisterUser, "POST", "/", tc.body, claims, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	for _, tc := range []struct {
		name, body, typ string
		user            *models.User
		err             error
		want            int
	}{
		{"reset", `{"password":"password1"}`, "email", &models.User{Active: true}, nil, 200},
		{"bad body", `{}`, "email", nil, nil, 400},
		{"short", `{"password":"short"}`, "email", nil, nil, 400},
		{"sms", `{"password":"password1"}`, "sms", nil, nil, 400},
		{"missing", `{"password":"password1"}`, "email", nil, repository.ErrNotFound, 404},
		{"load error", `{"password":"password1"}`, "email", nil, errors.New("db"), 500},
		{"disabled", `{"password":"password1"}`, "email", &models.User{}, nil, 403},
		{"update error", `{"password":"password1"}`, "email", &models.User{Active: true}, errors.New("db"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			users := &fakeStores{user: tc.user, err: tc.err}
			if tc.name == "update error" {
				users.err = nil
				users.updateErr = tc.err
			}
			h := &Handler{cfg: config.Config{EncryptionKey: key, PasswordKDF: "sha256"}, users: users}
			params := gin.Params{{Key: "type", Value: tc.typ}, {Key: "identifier", Value: "A@B"}}
			rec := handlerRequest(t, h.AdminResetPassword, "PUT", "/", tc.body, claims, params)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestOAuthConfigAndPortalWrappers(t *testing.T) {
	cfg := config.Config{JWTSecret: "secret", JWTExpiry: time.Hour, RefreshExpiry: 2 * time.Hour}
	tokens := auth.NewTokenService(cfg)
	h := &Handler{tokens: tokens}
	if rec := handlerRequest(t, h.OAuthConfig, "GET", "/", "", nil, nil); rec.Code != 200 {
		t.Fatalf("config=%d", rec.Code)
	}
	for _, call := range []gin.HandlerFunc{h.AdminLoginEmail, h.MemberLoginEmail, h.AdminOAuthToken, h.MemberOAuthToken} {
		if rec := handlerRequest(t, call, "POST", "/", `{}`, nil, nil); rec.Code != 400 {
			t.Fatalf("wrapper=%d", rec.Code)
		}
	}
}

func TestAuthenticationErrorBranches(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cfg := config.Config{
		EncryptionKey: key, PasswordKDF: "sha256", OTPExpiry: time.Minute,
		JWTSecret: "secret", JWTExpiry: time.Hour, RefreshExpiry: 2 * time.Hour,
	}
	tokens := auth.NewTokenService(cfg)
	baseDomain := &models.DomainSettings{Domain: "acme", EmailLoginEnabled: true, SMSLoginEnabled: true}
	active := &models.User{Domain: "acme", Type: "email", Identifier: "a@b", Active: true}
	tests := []struct {
		name, body string
		call       func(*Handler) gin.HandlerFunc
		user       *models.User
		userErr    error
		domain     *models.DomainSettings
		domainErr  error
		otpErr     error
		oauthErr   error
		want       int
	}{
		{"login malformed", `{`, func(h *Handler) gin.HandlerFunc { return h.LoginEmail }, nil, nil, baseDomain, nil, nil, nil, 400},
		{"login organization", `{"identifier":"a","password":"p"}`, func(h *Handler) gin.HandlerFunc { return h.LoginEmail }, nil, nil, baseDomain, nil, nil, nil, 400},
		{"login domain db", `{"domain":"acme","identifier":"a","password":"p"}`, func(h *Handler) gin.HandlerFunc { return h.LoginEmail }, nil, nil, nil, errors.New("db"), nil, nil, 500},
		{"login disabled method", `{"domain":"acme","identifier":"a","password":"p"}`, func(h *Handler) gin.HandlerFunc { return h.LoginEmail }, nil, nil, &models.DomainSettings{}, nil, nil, nil, 403},
		{"login db", `{"domain":"acme","identifier":"a","password":"p"}`, func(h *Handler) gin.HandlerFunc { return h.LoginEmail }, nil, errors.New("db"), baseDomain, nil, nil, nil, 500},
		{"login inactive", `{"domain":"acme","identifier":"a","password":"p"}`, func(h *Handler) gin.HandlerFunc { return h.LoginEmail }, &models.User{}, nil, baseDomain, nil, nil, nil, 403},
		{"otp request malformed", `{`, func(h *Handler) gin.HandlerFunc { return h.RequestOTP }, nil, nil, baseDomain, nil, nil, nil, 400},
		{"otp request organization", `{"identifier":"1"}`, func(h *Handler) gin.HandlerFunc { return h.RequestOTP }, nil, nil, baseDomain, nil, nil, nil, 400},
		{"otp request missing", `{"domain":"acme","identifier":"1"}`, func(h *Handler) gin.HandlerFunc { return h.RequestOTP }, nil, repository.ErrNotFound, baseDomain, nil, nil, nil, 401},
		{"otp request db", `{"domain":"acme","identifier":"1"}`, func(h *Handler) gin.HandlerFunc { return h.RequestOTP }, nil, errors.New("db"), baseDomain, nil, nil, nil, 500},
		{"otp request inactive", `{"domain":"acme","identifier":"1"}`, func(h *Handler) gin.HandlerFunc { return h.RequestOTP }, &models.User{}, nil, baseDomain, nil, nil, nil, 403},
		{"otp save", `{"domain":"acme","identifier":"1"}`, func(h *Handler) gin.HandlerFunc { return h.RequestOTP }, active, nil, baseDomain, nil, errors.New("redis"), nil, 500},
		{"verify malformed", `{`, func(h *Handler) gin.HandlerFunc { return h.VerifyOTP }, nil, nil, baseDomain, nil, nil, nil, 400},
		{"verify organization", `{"identifier":"1","otp":"1"}`, func(h *Handler) gin.HandlerFunc { return h.VerifyOTP }, nil, nil, baseDomain, nil, nil, nil, 400},
		{"verify user", `{"domain":"acme","identifier":"1","otp":"1"}`, func(h *Handler) gin.HandlerFunc { return h.VerifyOTP }, nil, errors.New("db"), baseDomain, nil, nil, nil, 401},
		{"verify inactive", `{"domain":"acme","identifier":"1","otp":"1"}`, func(h *Handler) gin.HandlerFunc { return h.VerifyOTP }, &models.User{}, nil, baseDomain, nil, nil, nil, 403},
		{"verify expired", `{"domain":"acme","identifier":"1","otp":"1"}`, func(h *Handler) gin.HandlerFunc { return h.VerifyOTP }, active, nil, baseDomain, nil, otp.ErrExpired, nil, 401},
		{"verify mismatch", `{"domain":"acme","identifier":"1","otp":"1"}`, func(h *Handler) gin.HandlerFunc { return h.VerifyOTP }, active, nil, baseDomain, nil, otp.ErrMismatch, nil, 401},
		{"verify db", `{"domain":"acme","identifier":"1","otp":"1"}`, func(h *Handler) gin.HandlerFunc { return h.VerifyOTP }, active, nil, baseDomain, nil, errors.New("redis"), nil, 500},
		{"oauth malformed", `{`, func(h *Handler) gin.HandlerFunc { return h.OAuthToken }, nil, nil, baseDomain, nil, nil, nil, 400},
		{"refresh malformed", `{`, func(h *Handler) gin.HandlerFunc { return h.OAuthRefresh }, nil, nil, baseDomain, nil, nil, nil, 400},
		{"refresh invalid", `{"refresh_token":"bad"}`, func(h *Handler) gin.HandlerFunc { return h.OAuthRefresh }, nil, nil, baseDomain, nil, nil, nil, 401},
		{"verify issue token error", `{"domain":"acme","identifier":"1","otp":"1"}`, func(h *Handler) gin.HandlerFunc { return h.VerifyOTP }, active, nil, baseDomain, nil, nil, errors.New("insert"), 500},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{
				cfg: cfg, tokens: tokens,
				users:   &fakeStores{user: tc.user, err: tc.userErr},
				domains: &fakeDomain{data: tc.domain, err: tc.domainErr},
				otps:    &fakeOTP{err: tc.otpErr}, oauth: &fakeOAuth{err: tc.oauthErr},
			}
			rec := handlerRequest(t, tc.call(h), "POST", "/", tc.body, nil, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestLogoutErrors(t *testing.T) {
	for _, tc := range []struct {
		family string
		err    error
		want   int
	}{{"", nil, 401}, {"f", errors.New("db"), 500}, {"f", nil, 200}} {
		h := &Handler{oauth: &fakeOAuth{err: tc.err}}
		rec := handlerRequest(t, h.Logout, "POST", "/", "", &models.Claims{FamilyID: tc.family}, nil)
		if rec.Code != tc.want {
			t.Fatalf("logout=%d want=%d", rec.Code, tc.want)
		}
	}
}

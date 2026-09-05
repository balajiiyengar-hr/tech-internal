package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"tech-internal/internal/models"
	"tech-internal/internal/repository"

	"github.com/gin-gonic/gin"
)

type deleteArgs struct {
	organizationID string
	domain         string
	userType       string
	identifier     string
}

type stubUserDeleter struct {
	mu   sync.Mutex
	last deleteArgs
	// calls counts every invocation, including the ones that lost a delete race.
	calls int
	fn    func(deleteArgs) error
}

func (s *stubUserDeleter) DeleteByIdentity(_ context.Context, organizationID, domain, userType, identifier string) error {
	args := deleteArgs{organizationID, domain, userType, identifier}
	s.mu.Lock()
	s.last = args
	s.calls++
	s.mu.Unlock()
	return s.fn(args)
}

func newDeleteRouter(deleter userDeleter, claims *models.Claims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{userDeleter: deleter}
	r := gin.New()
	r.DELETE("/admin/users/:type/:identifier", func(c *gin.Context) {
		c.Set("claims", claims)
	}, h.AdminDeleteUser)
	return r
}

func adminClaims() *models.Claims {
	return &models.Claims{
		OrganizationID: "11111111-1111-1111-1111-111111111111",
		Domain:         "techhr.com",
		Type:           models.UserTypeEmail,
		Identifier:     "admin@techhr.com",
	}
}

func TestAdminDeleteUserStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		repoErr    error
		wantStatus int
	}{
		{name: "deleted", repoErr: nil, wantStatus: http.StatusOK},
		{name: "missing user is not found", repoErr: repository.ErrNotFound, wantStatus: http.StatusNotFound},
		{
			// Only the sentinel downgrades the status; a lookalike message must not.
			name:       "error text mentioning not found stays internal",
			repoErr:    errors.New("resolve membership: " + repository.ErrNotFound.Error()),
			wantStatus: http.StatusInternalServerError,
		},
		{name: "last admin is a conflict", repoErr: repository.ErrLastAdmin, wantStatus: http.StatusConflict},
		{
			name:       "foreign key violation stays internal",
			repoErr:    errors.New(`ERROR: update or delete on table "organization_memberships" violates foreign key constraint (SQLSTATE 23503)`),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "connection failure stays internal",
			repoErr:    errors.New("failed to connect to host"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deleter := &stubUserDeleter{fn: func(deleteArgs) error { return tt.repoErr }}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/admin/users/sms/%2B15550001111", nil)

			newDeleteRouter(deleter, adminClaims()).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

// A wrapped ErrNotFound must not be reported as 404: the switch uses errors.Is,
// so only sentinel-carrying errors downgrade from 500.
func TestAdminDeleteUserWrappedNotFoundIsNotFound(t *testing.T) {
	deleter := &stubUserDeleter{
		fn: func(deleteArgs) error { return errWrap("resolve membership", repository.ErrNotFound) },
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/sms/%2B15550001111", nil)

	newDeleteRouter(deleter, adminClaims()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func errWrap(msg string, err error) error {
	return &wrapped{msg: msg, err: err}
}

type wrapped struct {
	msg string
	err error
}

func (w *wrapped) Error() string { return w.msg + ": " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }

func TestAdminDeleteUserNormalizesIdentifier(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		wantType       string
		wantIdentifier string
	}{
		{
			// k6 and the browser send encodeURIComponent("+1555..."), so the
			// leading plus arrives percent-encoded and must survive decoding.
			name:           "percent encoded sms plus",
			path:           "/admin/users/sms/%2B15550001111",
			wantType:       models.UserTypeSMS,
			wantIdentifier: "+15550001111",
		},
		{
			name:           "email is lowercased",
			path:           "/admin/users/email/Person@TechHR.com",
			wantType:       models.UserTypeEmail,
			wantIdentifier: "person@techhr.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deleter := &stubUserDeleter{fn: func(deleteArgs) error { return nil }}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, tt.path, nil)

			newDeleteRouter(deleter, adminClaims()).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			if deleter.last.userType != tt.wantType {
				t.Errorf("type = %q, want %q", deleter.last.userType, tt.wantType)
			}
			if deleter.last.identifier != tt.wantIdentifier {
				t.Errorf("identifier = %q, want %q", deleter.last.identifier, tt.wantIdentifier)
			}
		})
	}
}

// Tokens minted before the V2 rollout carry no organization id. The handler must
// still forward the domain so the repository can resolve the organization,
// rather than failing on an empty uuid.
func TestAdminDeleteUserForwardsDomainForLegacyClaims(t *testing.T) {
	deleter := &stubUserDeleter{fn: func(deleteArgs) error { return nil }}
	claims := adminClaims()
	claims.OrganizationID = ""
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/sms/%2B15550001111", nil)

	newDeleteRouter(deleter, claims).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if deleter.last.organizationID != "" || deleter.last.domain != "techhr.com" {
		t.Fatalf("got org=%q domain=%q, want org=\"\" domain=\"techhr.com\"",
			deleter.last.organizationID, deleter.last.domain)
	}
}

func TestAdminDeleteUserRejectsSelfDelete(t *testing.T) {
	deleter := &stubUserDeleter{fn: func(deleteArgs) error { return nil }}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/email/Admin@techhr.com", nil)

	newDeleteRouter(deleter, adminClaims()).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if deleter.calls != 0 {
		t.Fatalf("deleter called %d times, want 0", deleter.calls)
	}
}

// The mixed load test creates and deletes the same identifier from several VUs.
// Every loser of the race must observe 404, never 500.
func TestAdminDeleteUserConcurrentDeleteIsIdempotent(t *testing.T) {
	const goroutines = 32

	var mu sync.Mutex
	exists := true
	deleter := &stubUserDeleter{fn: func(deleteArgs) error {
		mu.Lock()
		defer mu.Unlock()
		if !exists {
			return repository.ErrNotFound
		}
		exists = false
		return nil
	}}
	router := newDeleteRouter(deleter, adminClaims())

	codes := make([]int, goroutines)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/admin/users/sms/%2B15550001111", nil)
			router.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()

	var ok, notFound, other int
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusNotFound:
			notFound++
		default:
			other++
		}
	}
	if ok != 1 {
		t.Errorf("got %d 200 responses, want exactly 1", ok)
	}
	if notFound != goroutines-1 {
		t.Errorf("got %d 404 responses, want %d", notFound, goroutines-1)
	}
	if other != 0 {
		t.Errorf("got %d responses outside {200,404}, want 0", other)
	}
	if deleter.calls != goroutines {
		t.Errorf("deleter called %d times, want %d", deleter.calls, goroutines)
	}
}

func TestAdminDeleteUserReportsInternalErrorBody(t *testing.T) {
	deleter := &stubUserDeleter{fn: func(deleteArgs) error { return errors.New("boom") }}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/sms/%2B15550001111", nil)

	newDeleteRouter(deleter, adminClaims()).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// The raw driver error must not leak to the client.
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("response body leaked the underlying error: %s", rec.Body.String())
	}
}

package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"tech-internal/internal/config"
	"tech-internal/internal/models"
	"tech-internal/internal/repository"

	"github.com/gin-gonic/gin"
)

type p0Members struct {
	createErr error
	updateErr error
	deleteErr error
}

func (*p0Members) DeleteByIdentity(context.Context, string, string, string, string) error {
	return nil
}
func (*p0Members) List(context.Context, string, int, int) ([]models.Member, int64, error) {
	return nil, 0, nil
}
func (*p0Members) Get(context.Context, string, string) (*models.Member, error) {
	return nil, repository.ErrNotFound
}
func (f *p0Members) Create(context.Context, string, *models.Member) error { return f.createErr }
func (f *p0Members) Update(context.Context, string, string, string, *bool, []string) error {
	return f.updateErr
}
func (f *p0Members) Delete(context.Context, string, string) error { return f.deleteErr }
func (*p0Members) ListRoles(context.Context, string, int, int) ([]models.OrganizationRole, int64, error) {
	return nil, 0, nil
}
func (*p0Members) CreateRole(context.Context, string, string, string, []string) (*models.OrganizationRole, error) {
	return nil, nil
}

func memberContext(t *testing.T, method, path, body string, h gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "org"}, {Key: "member_id", Value: "target"}}
	c.Set("claims", &models.Claims{OrganizationID: "org", UserID: "caller"})
	h(c)
	return rec
}

func TestCreateMemberGlobalIdentityConflictP0(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{
		cfg: config.Config{
			EncryptionKey: []byte("0123456789abcdef0123456789abcdef"),
			PasswordKDF:   "sha256",
		},
		members: &p0Members{createErr: repository.ErrConflict},
	}
	tests := []struct {
		name, body string
	}{
		{"same email globally", `{"display_name":"Duplicate","role_keys":["member"],"email":"used@example.com","password":"password1"}`},
		{"same phone globally", `{"display_name":"Duplicate","role_keys":["member"],"phone":"+15550001111"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := memberContext(t, http.MethodPost, "/api/v2/organizations/org/members", tt.body, h.CreateMember)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status=%d want=409 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLastAdminDeleteAndDemoteP0(t *testing.T) {
	gin.SetMode(gin.TestMode)
	members := &p0Members{deleteErr: repository.ErrLastAdmin, updateErr: repository.ErrLastAdmin}
	h := &Handler{members: members}
	tests := []struct {
		name, method, body string
		call               gin.HandlerFunc
	}{
		{"delete", http.MethodDelete, "", h.DeleteMember},
		{"demote", http.MethodPatch, `{"role_keys":["member"]}`, h.UpdateMember},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := memberContext(t, tt.method, "/api/v2/organizations/org/members/target", tt.body, tt.call)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status=%d want=409 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

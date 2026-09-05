package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tech-internal/internal/models"

	"github.com/gin-gonic/gin"
)

func TestPortalRoleEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		roles  []string
		portal string
		ok     bool
		status int
	}{
		{name: "admin accepted at admin", roles: []string{"org_admin"}, portal: "admin", ok: true},
		{name: "admin rejected at member", roles: []string{"org_admin"}, portal: "member", status: http.StatusForbidden},
		{name: "member accepted at member", roles: []string{"finance"}, portal: "member", ok: true},
		{name: "member rejected at admin", roles: []string{"member"}, portal: "admin", status: http.StatusForbidden},
		{name: "invalid portal", roles: []string{"member"}, portal: "other", status: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			got := enforcePortal(ctx, &models.User{Roles: tt.roles}, tt.portal)
			if got != tt.ok {
				t.Fatalf("got ok=%v, want %v", got, tt.ok)
			}
			if !tt.ok && rec.Code != tt.status {
				t.Fatalf("got status=%d, want %d", rec.Code, tt.status)
			}
		})
	}
}

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tech-internal/internal/auth"
	"tech-internal/internal/config"
	"tech-internal/internal/models"
	"tech-internal/internal/repository"

	"github.com/gin-gonic/gin"
)

type jtiStore struct {
	got string
	rec *models.OAuthToken
	err error
}

func (s *jtiStore) GetActiveByJTI(_ context.Context, jti string) (*models.OAuthToken, error) {
	s.got = jti
	return s.rec, s.err
}

func TestAuthLooksUpJTIAndRejectsRevokedAccessP0(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Config{JWTSecret: "middleware-secret", JWTExpiry: time.Hour, RefreshExpiry: time.Hour}
	tokens := auth.NewTokenService(cfg)
	user := &models.User{
		MembershipID: "member", OrganizationID: "org", IdentityID: "identity",
		Domain: "acme.test", Type: models.UserTypeEmail, Identifier: "person@acme.test",
	}
	pair, err := tokens.IssuePair(user, "")
	if err != nil {
		t.Fatal(err)
	}
	active := models.OAuthToken{
		JTI: pair.AccessJTI, FamilyID: pair.FamilyID, Kind: models.TokenKindAccess,
		Domain: user.Domain, Type: user.Type, Identifier: user.Identifier,
		MembershipID: user.MembershipID, IdentityID: user.IdentityID, OrganizationID: user.OrganizationID,
	}
	tests := []struct {
		name  string
		store *jtiStore
		want  int
	}{
		{"active", &jtiStore{rec: &active}, http.StatusNoContent},
		{"revoked", &jtiStore{err: repository.ErrNotFound}, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/", Auth(tokens, tt.store), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tt.want, rec.Body.String())
			}
			if tt.store.got != pair.AccessJTI {
				t.Fatalf("looked up jti=%q want=%q", tt.store.got, pair.AccessJTI)
			}
		})
	}
}

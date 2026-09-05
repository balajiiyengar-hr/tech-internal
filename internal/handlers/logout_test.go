package handlers_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"tech-internal/internal/auth"
	"tech-internal/internal/config"
	"tech-internal/internal/handlers"
	"tech-internal/internal/middleware"
	"tech-internal/internal/models"
	"tech-internal/internal/repository"

	"github.com/gin-gonic/gin"
)

type memoryTokenStore struct {
	mu     sync.Mutex
	tokens map[string]models.OAuthToken
}

func (s *memoryTokenStore) InsertPair(_ context.Context, access models.OAuthToken, _ string, refresh models.OAuthToken, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[access.JTI] = access
	s.tokens[refresh.JTI] = refresh
	return nil
}

func (s *memoryTokenStore) RotatePair(_ context.Context, _ string, _ models.OAuthToken, _ string, _ models.OAuthToken, _ string) (bool, error) {
	return false, nil
}

func (s *memoryTokenStore) GetActiveByJTI(_ context.Context, jti string) (*models.OAuthToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.tokens[jti]
	if !ok || token.RevokedAt != nil || !token.ExpiresAt.After(time.Now()) {
		return nil, repository.ErrNotFound
	}
	copy := token
	return &copy, nil
}

func (s *memoryTokenStore) RevokeFamily(_ context.Context, familyID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var count int64
	for jti, token := range s.tokens {
		if token.FamilyID == familyID && token.RevokedAt == nil {
			token.RevokedAt = &now
			s.tokens[jti] = token
			count++
		}
	}
	return count, nil
}

func TestLogoutRevokesAccessAndRefreshFamily(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Config{
		JWTSecret:     "logout-test-secret",
		JWTExpiry:     time.Hour,
		RefreshExpiry: 24 * time.Hour,
	}
	tokens := auth.NewTokenService(cfg)
	user := &models.User{
		Domain:      "techhr.com",
		Type:        models.UserTypeEmail,
		Identifier:  "admin@techhr.com",
		Role:        models.RoleAdmin,
		Active:      true,
		DisplayName: "Portal Admin",
	}
	pair, err := tokens.IssuePair(user, "")
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryTokenStore{tokens: make(map[string]models.OAuthToken)}
	if err := store.InsertPair(
		context.Background(),
		tokenRecord(user, pair.AccessJTI, pair.FamilyID, models.TokenKindAccess, pair.AccessExp),
		"",
		tokenRecord(user, pair.RefreshJTI, pair.FamilyID, models.TokenKindRefresh, pair.RefreshExp),
		"",
	); err != nil {
		t.Fatal(err)
	}

	h := handlers.New(cfg, nil, nil, nil, nil, nil, store, tokens)
	router := gin.New()
	router.POST("/api/v1/auth/logout", middleware.Authenticate(tokens), h.Logout)
	router.GET("/api/v1/me", middleware.Auth(tokens, store), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	router.POST("/api/v1/oauth/token/refresh", h.OAuthRefresh)

	assertStatus(t, router, http.MethodGet, "/api/v1/me", nil, pair.AccessToken, http.StatusOK)
	assertStatus(t, router, http.MethodPost, "/api/v1/auth/logout", nil, pair.AccessToken, http.StatusNoContent)
	assertStatus(t, router, http.MethodGet, "/api/v1/me", nil, pair.AccessToken, http.StatusUnauthorized)
	assertStatus(t, router, http.MethodPost, "/api/v1/oauth/token/refresh",
		[]byte(`{"refresh_token":"`+pair.RefreshToken+`"}`), "", http.StatusUnauthorized)
	assertStatus(t, router, http.MethodPost, "/api/v1/auth/logout", nil, pair.AccessToken, http.StatusNoContent)
}

func tokenRecord(user *models.User, jti, familyID, kind string, expiresAt time.Time) models.OAuthToken {
	return models.OAuthToken{
		JTI:        jti,
		FamilyID:   familyID,
		Domain:     user.Domain,
		Type:       user.Type,
		Identifier: user.Identifier,
		Kind:       kind,
		ExpiresAt:  expiresAt,
	}
}

func assertStatus(t *testing.T, handler http.Handler, method, path string, body []byte, bearer string, want int) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: got %d, want %d; body=%s", method, path, rec.Code, want, rec.Body.String())
	}
}

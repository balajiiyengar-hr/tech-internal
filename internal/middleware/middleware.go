package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"tech-internal/internal/auth"
	"tech-internal/internal/models"
	"tech-internal/internal/repository"
	"tech-internal/pkg/portalauth"

	"github.com/gin-gonic/gin"
)

type AccessTokenStore interface {
	GetActiveByJTI(ctx context.Context, jti string) (*models.OAuthToken, error)
}

// Authenticate verifies the Bearer token's signature, expiry, and access-token use.
// Use Auth for protected resources that must also enforce server-side revocation.
func Authenticate(tokens *auth.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := portalauth.BearerToken(c.GetHeader(portalauth.HeaderAuthorization))
		if err != nil {
			if errors.Is(err, portalauth.ErrMissingToken) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header"})
			return
		}

		claims, err := tokens.Parse(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set("claims", claims)
		c.Next()
	}
}

// Auth verifies the JWT and requires its jti to have an active access-token row.
func Auth(tokens *auth.TokenService, oauth AccessTokenStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := portalauth.BearerToken(c.GetHeader(portalauth.HeaderAuthorization))
		if err != nil {
			if errors.Is(err, portalauth.ErrMissingToken) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header"})
			return
		}

		claims, err := tokens.Parse(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		active, err := oauth.GetActiveByJTI(c.Request.Context(), claims.JTI)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid, expired, or revoked token"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not validate session"})
			return
		}
		if active.Kind != models.TokenKindAccess ||
			active.FamilyID != claims.FamilyID ||
			active.Domain != claims.Domain ||
			active.Type != claims.Type ||
			active.Identifier != claims.Identifier ||
			(claims.UserID != "" && active.MembershipID != "" && active.MembershipID != claims.UserID) ||
			(claims.IdentityID != "" && active.IdentityID != claims.IdentityID) ||
			(claims.OrganizationID != "" && active.OrganizationID != claims.OrganizationID) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid, expired, or revoked token"})
			return
		}

		c.Set("claims", claims)
		c.Next()
	}
}

func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get("claims")
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		claims, ok := v.(*models.Claims)
		if !ok || (!hasRole(claims, models.RoleOrgAdmin) && !hasPermission(claims, "members:write")) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}
		c.Next()
	}
}

func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get("claims")
		claims, valid := v.(*models.Claims)
		if !ok || !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		if !hasPermission(claims, permission) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "permission required: " + permission})
			return
		}
		c.Next()
	}
}

func hasRole(claims *models.Claims, role string) bool {
	if claims == nil {
		return false
	}
	for _, have := range claims.Roles {
		if strings.EqualFold(have, role) {
			return true
		}
	}
	return role == models.RoleOrgAdmin && claims.Role == models.RoleAdmin
}

func hasPermission(claims *models.Claims, permission string) bool {
	if claims == nil {
		return false
	}
	for _, have := range claims.Permissions {
		if have == permission || have == "*" || have == "*:*" {
			return true
		}
	}
	for _, have := range claims.Scopes {
		if have == permission || have == "*" || have == "*:*" {
			return true
		}
	}
	return claims.Role == models.RoleAdmin
}

func CORSMiddleware(allowed []string) gin.HandlerFunc {
	allowAll := len(allowed) == 0 || (len(allowed) == 1 && allowed[0] == "*")

	return func(c *gin.Context) {
		origin := strings.TrimRight(c.GetHeader("Origin"), "/")
		allowOrigin := ""
		if allowAll {
			if origin != "" {
				allowOrigin = origin
			} else {
				allowOrigin = "*"
			}
		} else {
			for _, o := range allowed {
				if strings.EqualFold(o, origin) {
					allowOrigin = origin
					break
				}
			}
		}

		if allowOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowOrigin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, traceparent, tracestate, baggage, X-Request-ID")
			c.Header("Access-Control-Expose-Headers", "traceparent, X-Request-ID")
			c.Header("Access-Control-Max-Age", "600")
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

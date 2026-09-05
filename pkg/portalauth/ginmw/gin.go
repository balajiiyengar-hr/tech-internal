package ginmw

import (
	"errors"
	"net/http"

	"tech-internal/pkg/portalauth"

	"github.com/gin-gonic/gin"
)

const ClaimsKey = portalauth.ContextKey

// Authenticate is @authenticate for Gin: JWT integrity + expiry.
func Authenticate(secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := portalauth.AuthenticateRequest(secret, c.Request)
		if err != nil {
			abortAuth(c, err)
			return
		}
		c.Set(ClaimsKey, claims)
		c.Request = c.Request.WithContext(portalauth.WithClaims(c.Request.Context(), claims))
		c.Next()
	}
}

// Authorize is @authorize for Gin. Must run after Authenticate.
func Authorize(rules ...portalauth.Rule) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromGin(c)
		if !ok {
			abortAuth(c, portalauth.ErrMissingToken)
			return
		}
		if err := portalauth.Authorize(claims, rules...); err != nil {
			abortAuth(c, err)
			return
		}
		c.Next()
	}
}

// ClaimsFromGin reads the verified payload.
func ClaimsFromGin(c *gin.Context) (*portalauth.Claims, bool) {
	v, ok := c.Get(ClaimsKey)
	if !ok {
		return portalauth.FromContext(c.Request.Context())
	}
	claims, ok := v.(*portalauth.Claims)
	return claims, ok && claims != nil
}

func abortAuth(c *gin.Context, err error) {
	status := http.StatusForbidden
	msg := err.Error()
	switch {
	case errors.Is(err, portalauth.ErrMissingToken):
		status = http.StatusUnauthorized
		msg = "missing authorization header"
	case errors.Is(err, portalauth.ErrInvalidToken), errors.Is(err, portalauth.ErrExpiredToken), errors.Is(err, portalauth.ErrWrongUse):
		status = http.StatusUnauthorized
		msg = "invalid or expired token"
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

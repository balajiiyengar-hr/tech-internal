package portalauth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrMissingToken = errors.New("missing authorization header")
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
	ErrWrongUse     = errors.New("token is not an access token")
	ErrForbidden    = errors.New("forbidden")
)

// Authenticate is the @authenticate equivalent: HMAC integrity + exp.
// It does not check roles, scopes, or resource access.
func Authenticate(secret []byte, tokenString string) (*Claims, error) {
	claims, err := ParseSigned(secret, tokenString)
	if err != nil {
		return nil, err
	}
	if claims.TokenUse != "" && !strings.EqualFold(claims.TokenUse, TokenUseAccess) {
		return nil, ErrWrongUse
	}
	return claims, nil
}

// ParseSigned verifies HMAC + exp only. Callers that accept refresh tokens use this
// then check token_use themselves.
func ParseSigned(secret []byte, tokenString string) (*Claims, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("%w: empty jwt secret", ErrInvalidToken)
	}
	tokenString = strings.TrimSpace(tokenString)
	if tokenString == "" {
		return nil, ErrMissingToken
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(2*time.Second),
	)

	token, err := parser.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrInvalidToken)
		}
		return secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidToken, err.Error())
	}
	if !token.Valid {
		return nil, ErrInvalidToken
	}

	m, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrInvalidToken
	}

	claims := claimsFromMap(m)
	if claims.UserID == "" {
		claims.UserID = UserID(claims.Domain, claims.Type, claims.Identifier)
	}
	return claims, nil
}

// BearerToken extracts the raw JWT from an Authorization header.
func BearerToken(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", ErrMissingToken
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", ErrInvalidToken
	}
	return strings.TrimSpace(parts[1]), nil
}

// AuthenticateRequest is @authenticate for net/http: header + integrity + exp.
func AuthenticateRequest(secret []byte, r *http.Request) (*Claims, error) {
	raw, err := BearerToken(r.Header.Get(HeaderAuthorization))
	if err != nil {
		return nil, err
	}
	return Authenticate(secret, raw)
}

func claimsFromMap(m jwt.MapClaims) *Claims {
	c := &Claims{
		UserID:           claimString(m, "sub"),
		PersonID:         claimString(m, "person_id"),
		OrganizationID:   claimString(m, "organization_id"),
		OrganizationSlug: claimString(m, "org_slug"),
		IdentityID:       claimString(m, "identity_id"),
		Domain:           claimString(m, "domain"),
		Type:             claimString(m, "type"),
		Identifier:       claimString(m, "identifier"),
		Role:             claimString(m, "role"),
		Roles:            claimStringSlice(m, "roles"),
		Permissions:      claimStringSlice(m, "permissions"),
		Display:          claimString(m, "display_name"),
		Audience:         claimString(m, "aud"),
		JTI:              claimString(m, "jti"),
		TokenUse:         claimString(m, "token_use"),
		FamilyID:         claimString(m, "family_id"),
		Scopes:           claimStringSlice(m, "scopes"),
	}
	if n, ok := claimUnix(m, "exp"); ok {
		c.ExpiresAt = time.Unix(n, 0)
	}
	if n, ok := claimUnix(m, "iat"); ok {
		c.IssuedAt = time.Unix(n, 0)
	}
	return c
}

func claimString(m jwt.MapClaims, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func claimStringSlice(m jwt.MapClaims, key string) []string {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return strings.Split(t, " ")
	default:
		return nil
	}
}

func claimUnix(m jwt.MapClaims, key string) (int64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case jsonNumber:
		n, err := t.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

// jsonNumber is implemented by encoding/json.Number without importing json here.
type jsonNumber interface {
	Int64() (int64, error)
}

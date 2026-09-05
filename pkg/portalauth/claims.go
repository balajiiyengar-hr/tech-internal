package portalauth

import (
	"strings"
	"time"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	TokenUseAccess  = "access"
	TokenUseRefresh = "refresh"

	Audience = "tech-internal"

	HeaderAuthorization = "Authorization"
	ContextKey          = "portalauth.claims"
)

// Claims is the verified JWT payload other services authorize against.
type Claims struct {
	UserID           string    `json:"sub"`
	PersonID         string    `json:"person_id"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationSlug string    `json:"org_slug"`
	IdentityID       string    `json:"identity_id"`
	Domain           string    `json:"domain"`
	Type             string    `json:"type"`
	Identifier       string    `json:"identifier"`
	Role             string    `json:"role"`
	Roles            []string  `json:"roles"`
	Permissions      []string  `json:"permissions"`
	Display          string    `json:"display_name"`
	Scopes           []string  `json:"scopes"`
	Audience         string    `json:"aud"`
	JTI              string    `json:"jti"`
	TokenUse         string    `json:"token_use"`
	FamilyID         string    `json:"family_id"`
	ExpiresAt        time.Time `json:"exp"`
	IssuedAt         time.Time `json:"iat"`
}

// UserID builds the stable subject used as JWT "sub".
func UserID(domain, userType, identifier string) string {
	return strings.ToLower(strings.TrimSpace(domain)) + "/" +
		strings.ToLower(strings.TrimSpace(userType)) + "/" +
		strings.TrimSpace(identifier)
}

// ScopesForRole is the default scope set the auth server stamps on tokens.
func ScopesForRole(role string) []string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleAdmin:
		return []string{
			"profile:read",
			"apps:read",
			"apps:write",
			"users:read",
			"users:write",
			"domain:read",
			"domain:write",
		}
	default:
		return []string{
			"profile:read",
			"apps:read",
		}
	}
}

// EffectiveScopes returns token scopes, or the role default for older JWTs.
func (c *Claims) EffectiveScopes() []string {
	if c == nil {
		return nil
	}
	if len(c.Scopes) > 0 {
		return c.Scopes
	}
	if len(c.Permissions) > 0 {
		return c.Permissions
	}
	return ScopesForRole(c.Role)
}

func (c *Claims) HasRole(role string) bool {
	if c == nil {
		return false
	}
	return strings.EqualFold(c.Role, role)
}

func (c *Claims) HasScope(want string) bool {
	if c == nil || want == "" {
		return false
	}
	for _, have := range c.EffectiveScopes() {
		if scopeCovers(have, want) {
			return true
		}
	}
	return false
}

func (c *Claims) CanAccess(resource, action string) bool {
	resource = strings.TrimSpace(resource)
	action = strings.TrimSpace(action)
	if resource == "" || action == "" {
		return false
	}
	return c.HasScope(resource + ":" + action)
}

func scopeCovers(have, want string) bool {
	have = strings.ToLower(strings.TrimSpace(have))
	want = strings.ToLower(strings.TrimSpace(want))
	if have == "" || want == "" {
		return false
	}
	if have == "*" || have == "*:*" || have == want {
		return true
	}
	hRes, hAct, hOK := splitScope(have)
	wRes, wAct, wOK := splitScope(want)
	if !hOK || !wOK {
		return false
	}
	resOK := hRes == "*" || hRes == wRes
	actOK := hAct == "*" || hAct == wAct
	return resOK && actOK
}

func splitScope(s string) (resource, action string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

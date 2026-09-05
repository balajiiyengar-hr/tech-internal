package portalauth

import (
	"fmt"
	"strings"
)

// Rule is one @authorize check against a verified payload.
type Rule func(*Claims) error

// Authorize is the @authorize equivalent. claims must already have passed
// Authenticate. Every rule must succeed.
func Authorize(claims *Claims, rules ...Rule) error {
	if claims == nil {
		return fmt.Errorf("%w: missing claims", ErrForbidden)
	}
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		if err := rule(claims); err != nil {
			return err
		}
	}
	return nil
}

// RequireRole allows any of the listed roles (OR).
func RequireRole(roles ...string) Rule {
	wanted := normalize(roles)
	return func(c *Claims) error {
		for _, role := range wanted {
			if c.HasRole(role) {
				return nil
			}
		}
		return fmt.Errorf("%w: role %q not in %v", ErrForbidden, c.Role, wanted)
	}
}

// RequireScope requires every listed scope (AND). Wildcards on the token apply.
func RequireScope(scopes ...string) Rule {
	wanted := normalize(scopes)
	return func(c *Claims) error {
		for _, scope := range wanted {
			if !c.HasScope(scope) {
				return fmt.Errorf("%w: missing scope %s", ErrForbidden, scope)
			}
		}
		return nil
	}
}

// RequireAnyScope requires at least one listed scope (OR).
func RequireAnyScope(scopes ...string) Rule {
	wanted := normalize(scopes)
	return func(c *Claims) error {
		for _, scope := range wanted {
			if c.HasScope(scope) {
				return nil
			}
		}
		return fmt.Errorf("%w: missing any scope in %v", ErrForbidden, wanted)
	}
}

// RequireResource checks whether the token user may perform action on resource
// via scope "{resource}:{action}" (and wildcards).
func RequireResource(resource, action string) Rule {
	return RequireScope(strings.TrimSpace(resource) + ":" + strings.TrimSpace(action))
}

// RequireUserID checks JWT sub / constructed user id against the resource owner.
func RequireUserID(userID string) Rule {
	userID = strings.TrimSpace(userID)
	return func(c *Claims) error {
		if c.UserID == userID || UserID(c.Domain, c.Type, c.Identifier) == userID {
			return nil
		}
		return fmt.Errorf("%w: user %s cannot access resource owned by %s", ErrForbidden, c.UserID, userID)
	}
}

// RequireDomain pins the token to an organization domain.
func RequireDomain(domain string) Rule {
	domain = strings.ToLower(strings.TrimSpace(domain))
	return func(c *Claims) error {
		if strings.EqualFold(c.Domain, domain) {
			return nil
		}
		return fmt.Errorf("%w: domain %s cannot access %s", ErrForbidden, c.Domain, domain)
	}
}

// RequireAudience checks aud when the resource names an API audience.
func RequireAudience(aud string) Rule {
	aud = strings.TrimSpace(aud)
	return func(c *Claims) error {
		if c.Audience == "" || strings.EqualFold(c.Audience, aud) {
			return nil
		}
		return fmt.Errorf("%w: audience %s cannot access %s", ErrForbidden, c.Audience, aud)
	}
}

func normalize(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

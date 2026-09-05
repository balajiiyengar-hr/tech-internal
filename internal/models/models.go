package models

import "time"

const (
	UserTypeEmail = "email"
	UserTypeSMS   = "sms"
	RoleAdmin     = "admin"
	RoleUser      = "user"
	RoleOrgAdmin  = "org_admin"
)

type User struct {
	PersonID       string    `json:"person_id"`
	MembershipID   string    `json:"membership_id"`
	IdentityID     string    `json:"identity_id"`
	OrganizationID string    `json:"organization_id"`
	Domain         string    `json:"domain"`
	Type           string    `json:"type"`
	Identifier     string    `json:"identifier"`
	Role           string    `json:"role"`
	Roles          []string  `json:"roles"`
	Permissions    []string  `json:"permissions"`
	Active         bool      `json:"active"`
	DisplayName    string    `json:"display_name"`
	PasswordHash   *string   `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

type IdentityInput struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
	Password   string `json:"password,omitempty"`
}

type Member struct {
	ID             string          `json:"id"`
	PersonID       string          `json:"person_id"`
	OrganizationID string          `json:"organization_id"`
	Organization   string          `json:"organization"`
	DisplayName    string          `json:"display_name"`
	Active         bool            `json:"active"`
	RoleKeys       []string        `json:"role_keys"`
	Permissions    []string        `json:"permissions,omitempty"`
	Identities     []IdentityInput `json:"identities"`
	CreatedAt      time.Time       `json:"created_at"`
}

type OrganizationRole struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	IsSystem    bool     `json:"is_system"`
	Permissions []string `json:"permissions"`
}

type DomainSettings struct {
	Domain            string `json:"domain"`
	EmailLoginEnabled bool   `json:"email_login_enabled"`
	SMSLoginEnabled   bool   `json:"sms_login_enabled"`
}

type PortalApp struct {
	ID        string `json:"id"`
	Domain    string `json:"domain"`
	Section   string `json:"section"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	IconURL   string `json:"icon_url,omitempty"`
	SortOrder int    `json:"sort_order"`
	Active    bool   `json:"active"`
}

type Claims struct {
	UserID           string   `json:"user_id"`
	PersonID         string   `json:"person_id"`
	OrganizationID   string   `json:"organization_id"`
	OrganizationSlug string   `json:"org_slug"`
	IdentityID       string   `json:"identity_id"`
	Domain           string   `json:"domain"`
	Type             string   `json:"type"`
	Identifier       string   `json:"identifier"`
	Role             string   `json:"role"`
	Roles            []string `json:"roles"`
	Permissions      []string `json:"permissions"`
	Display          string   `json:"display_name"`
	Scopes           []string `json:"scopes"`
	JTI              string   `json:"jti"`
	TokenUse         string   `json:"token_use"`
	FamilyID         string   `json:"family_id"`
}

const (
	TokenKindAccess  = "access"
	TokenKindRefresh = "refresh"
)

type OAuthToken struct {
	ID             string     `json:"id"`
	JTI            string     `json:"jti"`
	FamilyID       string     `json:"family_id"`
	Domain         string     `json:"domain"`
	Type           string     `json:"type"`
	Identifier     string     `json:"identifier"`
	MembershipID   string     `json:"membership_id"`
	IdentityID     string     `json:"identity_id"`
	OrganizationID string     `json:"organization_id"`
	Kind           string     `json:"kind"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

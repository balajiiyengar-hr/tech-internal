# portalauth V2 claims

`tech-internal/pkg/portalauth` verifies Aperture HS256 access JWTs for other Go
services. `Authenticate` checks signature, required expiry, and access-token
use. `Authorize` applies role/scope/resource rules.

## Claims

```json
{
  "sub": "<membership-uuid>",
  "person_id": "<person-uuid>",
  "organization_id": "<organization-uuid>",
  "org_slug": "techhr.com",
  "identity_id": "<identity-uuid>",
  "roles": ["org_admin"],
  "permissions": ["profile:read", "apps:read", "members:write"],
  "domain": "techhr.com",
  "type": "email",
  "identifier": "admin@techhr.com",
  "role": "admin",
  "scopes": ["profile:read", "apps:read", "members:write"],
  "jti": "<token-id>",
  "token_use": "access",
  "family_id": "<family-id>",
  "aud": "tech-internal",
  "exp": 1757034000,
  "iat": 1756947600
}
```

`sub` is the organization membership ID, not a global person or a composite
email key. Use `organization_id` for hard tenant boundaries. `domain`, `type`,
`identifier`, `role`, and `scopes` are V1 compatibility aliases.

`EffectiveScopes` prefers `scopes`, then V2 `permissions`, then legacy role
defaults. New integrations should authorize with permission names:
`profile:read`, `apps:read`, `apps:write`, `members:read`, `members:write`,
`roles:read`, `roles:write`, `organization:read`, and
`organization:write`.

## Example

```go
claims, err := portalauth.Authenticate([]byte(os.Getenv("JWT_SECRET")), raw)
if err != nil {
    // 401
}
if claims.OrganizationID != resourceOrganizationID {
    // 403: cross-organization
}
if err := portalauth.Authorize(claims,
    portalauth.RequireScope("members:read"),
); err != nil {
    // 403
}
```

Gin users can use `pkg/portalauth/ginmw`. Always authenticate before
authorization.

## Revocation boundary

The package performs local cryptographic verification only. Aperture's own
`internal/middleware.Auth` additionally checks the active `oauth_tokens.jti`
and verifies membership, identity, and organization bindings. A separate
service must add a shared token-store lookup or introspection if logout must
take effect immediately.

Refresh tokens (`token_use=refresh`) are rejected by access authentication.
`POST /api/v1/auth/logout` and `/oauth/revoke` revoke the full token family.

HTTP/token details: [OAuth.md](../OAuth.md). Scale/data rules:
[api-rules.md](../api-rules.md).

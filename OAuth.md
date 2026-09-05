# Aperture OAuth access and refresh tokens

Local base URL: `http://127.0.0.1:8080/api/v1`.

## Portal-specific grants

- `POST /oauth/token/member`: only non-`org_admin` memberships.
- `POST /oauth/token/admin`: only `org_admin` memberships.
- `POST /oauth/token`: V1-compatible grant; send `portal=member|admin` when
  portal enforcement is required.

Request:

```json
{
  "organization": "techhr.com",
  "identifier": "admin@techhr.com",
  "password": "Admin@123",
  "portal": "admin"
}
```

`domain` remains an alias for `organization`. A correct identity with the
wrong organization returns `401`. A correct credential on the wrong portal
returns `403` with a clear member/admin login message.

The email login endpoints return the same response:

```json
{
  "token_type": "Bearer",
  "access_token": "<jwt>",
  "refresh_token": "<jwt>",
  "expires_in": 86400,
  "refresh_expires_in": 345600,
  "token": "<access-token alias>",
  "user": {
    "membership_id": "uuid",
    "person_id": "uuid",
    "organization_id": "uuid",
    "organization": "techhr.com",
    "identity_id": "uuid",
    "domain": "techhr.com",
    "type": "email",
    "identifier": "admin@techhr.com",
    "role": "admin",
    "roles": ["org_admin"],
    "permissions": ["profile:read", "apps:read", "members:write"]
  }
}
```

## Claims

| Claim | Meaning |
|---|---|
| `sub` | Membership UUID |
| `person_id` | Person UUID |
| `organization_id`, `org_slug` | Bound organization |
| `identity_id` | Login identity used |
| `roles`, `permissions` | Effective organization authorization |
| `domain`, `type`, `identifier`, `role`, `scopes` | V1 aliases |
| `jti`, `family_id` | Revocation/token family |
| `token_use` | `access` or `refresh` |
| `aud`, `exp`, `iat` | Audience and times |

Access and refresh rows also persist `membership_id`, `identity_id`, and
`organization_id`. Protected Aperture routes validate JWT signature/expiry and
the active persisted access `jti`.

## Refresh

`POST /oauth/token/refresh` with:

```json
{"refresh_token":"<refresh-jwt>"}
```

Refresh tokens are single-use. Rotation checks the persisted identity,
membership, and organization bindings, revokes the old refresh token, and
stores a new pair. Expired, revoked, unknown, or reused refresh tokens return
`401`.

## Logout

`POST /auth/logout` or `POST /oauth/revoke` with the access bearer token
revokes all tokens in its `family_id` and returns `204`. Subsequent protected
use and refresh return `401`.

Token TTLs are `ACCESS_TOKEN_HOURS` (fallback `JWT_EXPIRY_HOURS`, default 24)
and `REFRESH_TOKEN_HOURS` (default 96). `GET /oauth/config` reports effective
values.

Machine-readable contract: [OAuth.yaml](./OAuth.yaml). Library integration:
[docs/portalauth.md](./docs/portalauth.md).

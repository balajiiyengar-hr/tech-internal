# Aperture API and storage reference

API origin defaults to `http://127.0.0.1:8080`; the separate UI defaults to
`http://127.0.0.1:3100`. JSON requests use `Content-Type: application/json`.
Protected requests use `Authorization: Bearer <access_token>`.

## List pagination

The list endpoints `GET /api/v1/apps`, `GET /api/v1/admin/users`,
`GET /api/v2/organizations/{id}/members`, and
`GET /api/v2/organizations/{id}/roles` accept `pageSize` and `page`.
`PageSize` and `PageVal` remain supported as compatibility aliases. Page size
defaults to 25 and values above 60 are clamped to 60; page defaults to 1.

Each response contains:

```json
{
  "pagination": {
    "page_size": 25,
    "page_val": 1,
    "total": 42,
    "has_next": true
  }
}
```

## Login

Email request body:

```json
{
  "organization": "techhr.com",
  "identifier": "admin@techhr.com",
  "password": "Admin@123",
  "portal": "admin"
}
```

`domain` is accepted as a V1 alias for `organization`.

- `POST /api/v1/auth/login/member/email`: requires a non-org-admin membership.
- `POST /api/v1/auth/login/admin/email`: requires `org_admin`.
- `POST /api/v1/auth/login/email`: legacy route; optional `portal` hint.
- `POST /api/v1/oauth/token/member` and `/oauth/token/admin`: equivalent
  password grants.
- `POST /api/v1/oauth/token`: legacy password grant.
- `POST /api/v1/auth/otp/request` and `/auth/otp/verify`: include
  `portal: "member"` or `"admin"`.

Invalid password, unknown identity, or correct identity with the wrong
organization returns `401`. Correct credentials at the wrong portal return
`403` with directions to the proper login. Disabled membership/method returns
`403`.

## Token response and session

Login returns access/refresh tokens and:

```json
{
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
    "permissions": ["members:read", "members:write"]
  }
}
```

Refresh with `POST /api/v1/oauth/token/refresh`. Logout with
`POST /api/v1/auth/logout` or `/oauth/revoke`; both revoke the entire token
family. Protected routes validate the active persisted `jti`.

## Member management

All IDs in these paths must match the caller's `organization_id`.

`GET /api/v2/organizations/{id}/members` lists membership aggregates (person,
identities, role keys, status). Requires `members:read`.

`POST /api/v2/organizations/{id}/members` requires `members:write`:

```json
{
  "display_name": "Nina Patel",
  "role_keys": ["project_manager"],
  "email": "nina@example.com",
  "password": "ChangeMe1!",
  "phone": "+14155550199"
}
```

At least one identity is required. Email requires an 8+ character password.
Email and phone may both be attached to the new person's single membership.
An identity already used anywhere returns `409`.

`PATCH /api/v2/organizations/{id}/members/{membership_id}` accepts:

```json
{
  "display_name": "Nina P.",
  "active": true,
  "role_keys": ["finance"]
}
```

`DELETE` on the same path removes the member and identities. Disabling,
deleting, or demoting the last active org admin returns `409`. Cross-org
organization paths return `403`; a membership that belongs to another
organization returns `404`. `role_keys` must contain at least one available
organization role. Unknown role keys return `400` with the rejected key and do
not change the member's existing assignments.

## Roles

- `GET /api/v2/organizations/{id}/roles` (`roles:read`)
- `POST /api/v2/organizations/{id}/roles` (`roles:write`)

Create request:

```json
{
  "key": "auditor",
  "name": "Auditor",
  "permissions": ["profile:read", "apps:read", "members:read"]
}
```

System roles per organization: `org_admin`, `member`, `project_manager`,
`contractor`, `finance`. `org_admin` has every catalog permission; the other
seed roles have `profile:read` and `apps:read`.

## Storage model

`migrations/004_v2_identity.sql` adds:

- `organizations`
- `persons`
- `organization_memberships` (unique `person_id`, and
  `(organization_id, person_id)`)
- `login_identities` with global unique index on
  `(type, lower(identifier))`
- `organization_roles`, `permissions`, `organization_role_permissions`,
  `membership_roles`
- organization/membership/identity bindings on apps and OAuth tokens

Application-created IDs use `internal/id.NewUUIDv7`. Existing V1 rows are
idempotently backfilled from `domains`, `users`, `portal_apps`, and
`oauth_tokens`. V1 tables/routes remain compatibility surfaces.

Postgres access uses the shared `pgxpool`; child identity/role writes are
batched inside one transaction. OTP is one atomic Redis `GETDEL`, so it does
not need a pipeline. Multi-command Redis additions must pipeline per
[api-rules.md](./api-rules.md).

## V1 compatibility

Existing `/api/v1/admin/users`, `/admin/domain`, `/admin/apps`, `/me`, and
`/apps` remain available. JWT aliases `domain`, `type`, `identifier`, `role`,
and `scopes` remain during dual issue.

Seed: organization `techhr.com`, org admin `admin@techhr.com`, password
`Admin@123`.

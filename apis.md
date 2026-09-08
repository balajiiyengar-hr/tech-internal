# Aperture API route contract

Runtime prefixes are `/api/v1` (authentication, sessions, compatibility) and
`/api/v2` (organization identity management).

All list `GET`s (`/apps`, `/admin/users`, and V2 `/members` and `/roles`) use
`pageSize` (default 25, clamped to 60) and `page` (default 1). Legacy
`PageSize`/`PageVal` names are also accepted. Responses include
`pagination.page_size`, `page_val`, `total`, and `has_next`.

## Public V1

| Method | Path | Result |
|---|---|---|
| GET | `/health` | Health |
| GET | `/auth/domain?domain=` | Organization login methods |
| POST | `/auth/login/member/email` | Member-only password login |
| POST | `/auth/login/admin/email` | Org-admin-only password login |
| POST | `/auth/login/email` | Legacy login; optional portal hint |
| POST | `/auth/otp/request` | Issue SMS OTP; body includes portal |
| POST | `/auth/otp/verify` | Consume OTP and enforce portal |
| POST | `/oauth/token/member` | Member password token grant |
| POST | `/oauth/token/admin` | Admin password token grant |
| POST | `/oauth/token` | Legacy password token grant |
| POST | `/oauth/token/refresh` | Rotate refresh token |

Login accepts either `organization` or legacy `domain`. Organization is always
required. Wrong organization returns `401`; portal-role mismatch returns `403`.

## Authenticated V1

| Method | Path | Permission/behavior |
|---|---|---|
| POST | `/auth/logout`, `/oauth/revoke` | Revoke session family |
| GET | `/me` | Membership and identity claims |
| GET | `/apps` | Organization apps |
| GET/PUT | `/admin/domain` | Legacy org-admin settings |
| GET/POST | `/admin/users` | Legacy member adapter |
| PUT | `/admin/users/{type}/{identifier}/password` | Reset email password |
| DELETE | `/admin/users/{type}/{identifier}` | Delete membership; last-admin protected |
| POST/DELETE | `/admin/apps[/{id}]` | Organization app management |

## V2 organization APIs

| Method | Path | Required permission |
|---|---|---|
| GET | `/api/v2/organizations/{id}/members` | `members:read` |
| POST | `/api/v2/organizations/{id}/members` | `members:write` |
| PATCH | `/api/v2/organizations/{id}/members/{membership_id}` | `members:write` |
| DELETE | `/api/v2/organizations/{id}/members/{membership_id}` | `members:write` |
| GET | `/api/v2/organizations/{id}/roles` | `roles:read` |
| POST | `/api/v2/organizations/{id}/roles` | `roles:write` |

The `{id}` path value must equal the JWT `organization_id`; mismatch is `403`.

Member create:

```json
{
  "display_name": "Sam Lee",
  "role_keys": ["contractor"],
  "email": "sam@example.com",
  "password": "ChangeMe1!",
  "phone": "+14155550123"
}
```

Member update:

```json
{"display_name":"Sam L.","active":false,"role_keys":["finance"]}
```

`role_keys` replaces the member's role assignments when present and must
contain at least one role available in the organization. An unknown role
returns `400` without changing existing assignments. A membership owned by a
different organization returns `404`; demoting the only active `org_admin`
returns `409`.

Role create:

```json
{"key":"auditor","name":"Auditor","permissions":["profile:read","apps:read"]}
```

## Status behavior

- `400`: malformed body, missing identity, invalid role/permission.
- `401`: bad/expired/revoked credentials, including identity paired with the
  wrong organization.
- `403`: wrong portal, missing permission, disabled account/method, or
  cross-organization access.
- `404`: unknown organization/member.
- `409`: globally duplicate email/phone, duplicate role key, or last-admin
  protection.

## Error responses

Every error body is `{"error": "<message>", "custom_error_code": "<CODE>"}`.
`error` is a human-readable message; `custom_error_code` is a stable, bounded
enum (defined in `internal/apierr`) safe to group and alert on — it never
carries free text, identifiers, or organization IDs. `custom_error_code` is
also published as a Prometheus label (alongside a `service` tag) on
`tech_internal_api_http_requests_total`/`..._request_duration_seconds`, in
access logs as `error.code`, and as the `error.code` span attribute, so the
same value correlates metrics, logs, and traces for one failure. Codes are
grouped by concern: `AUTH_*` (missing/invalid/expired/revoked token, wrong
portal, disabled account or login method, bad credentials), `OTP_*` (expired,
invalid, unknown identifier), `VALIDATION_ERROR` (malformed/missing input),
`FORBIDDEN_*` (admin/permission/cross-organization), `NOT_FOUND` /
`ROUTE_NOT_FOUND`, `CONFLICT_*` (duplicate, last-admin) / `SELF_ACTION_BLOCKED`,
`ROLE_*` / `PERMISSION_UNKNOWN`, and `INTERNAL_*` (DB, cache, token, crypto,
or unclassified) for 5xx responses.

## Token claims

`sub=membership_id`; V2 claims are `person_id`, `organization_id`, `org_slug`,
`identity_id`, `roles`, and `permissions`. V1 aliases remain: `domain`, `type`,
`identifier`, `role`, and `scopes`. Tokens also include `aud`, `jti`,
`token_use`, `family_id`, `exp`, and `iat`.

Detailed payloads: [api-docs.md](./api-docs.md). OAuth:
[OAuth.md](./OAuth.md). Engineering requirements:
[api-rules.md](./api-rules.md).

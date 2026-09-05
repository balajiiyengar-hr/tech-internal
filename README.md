# Aperture Tech Internal Portal

**New teammate? Start with the [new-machine setup guide](./new-setup.md).**

Aperture is an organization-scoped internal app launcher with separate member
and org-admin portals, email/password and SMS OTP identities, role-based
permissions, and revocable OAuth access/refresh sessions.

**Mandatory contributor contract:** every API/backend change must follow
[api-rules.md](./api-rules.md), including pooled data access, batched writes,
query analysis, and the mixed 200 TPS performance gate.

## Run locally

```bash
docker compose up -d --build
```

Or run dependencies separately and use:

```bash
./scripts/run-api.sh
./scripts/run-ui.sh
```

Defaults: UI `http://127.0.0.1:3100`, API
`http://127.0.0.1:8080`, Postgres `5434`, Redis `6380`, Prometheus
`9090`, Grafana `3200`, Jaeger UI `16687`, Jaeger OTLP HTTP `4319`, and
Jaeger OTLP gRPC `4320`. These non-default Jaeger host ports avoid collisions
with other local tracing stacks. Do not start host API/UI processes
while compose owns ports 8080/3100.

## UI routes

- `/` and `/login`: member sign-in; successful login opens `/dashboard.html`.
- `/admin/login`: org-admin sign-in; successful login opens `/admin.html`.
- `/dashboard.html`: member application launcher.
- `/admin.html`: organization members, roles, login settings, and apps.

Both login portals require organization slug plus the member/admin's own email
or phone identity. The backend rejects members at the admin portal and org
admins at the member portal with `403` and a portal-specific message.

Seeded org admin:

- Organization: `techhr.com`
- Email: `admin@techhr.com`
- Password: `Admin@123`
- Login URL: `http://127.0.0.1:3100/admin/login`

The first API boot seals the seed password. Do not change these development
credentials in migrations.

## V2 identity rules

- A person has exactly one organization membership.
- An email or phone identity belongs to one person in one organization.
- `(type, lower(identifier))` is globally unique. Reusing an email/phone in the
  same or another organization returns `409`.
- Login still requires the organization slug. A valid identifier paired with
  the wrong organization returns `401`, so account existence is not leaked.
- Memberships may have system roles (`org_admin`, `member`,
  `project_manager`, `contractor`, `finance`) or custom organization roles.
- The last active `org_admin` cannot be disabled, deleted, or demoted (`409`).

`migrations/004_v2_identity.sql` backfills V1 domains/users/tokens/apps. Legacy
`/api/v1` routes remain adapters using `domain` as organization slug; login
bodies also accept `organization`.

## API overview

Public and session routes use `/api/v1`:

- `POST /auth/login/member/email`, `POST /auth/login/admin/email`
- `POST /auth/otp/request`, `POST /auth/otp/verify` with
  `portal=member|admin`
- Legacy `POST /auth/login/email` and `POST /oauth/token`
- `POST /oauth/token/member`, `POST /oauth/token/admin`
- `POST /oauth/token/refresh`, `POST /auth/logout`, `POST /oauth/revoke`
- `GET /me`, `GET /apps`
- Legacy `/admin/users`, `/admin/domain`, `/admin/apps`

V2 organization management uses `/api/v2`:

- `GET|POST /organizations/{organization_id}/members`
- `PATCH|DELETE /organizations/{organization_id}/members/{membership_id}`
- `GET|POST /organizations/{organization_id}/roles`

Cross-organization IDs are denied. Member routes require `members:read` or
`members:write`; role routes require `roles:read` or `roles:write`.

JWT `sub` is `membership_id`. Tokens also carry `person_id`,
`organization_id`, `org_slug`, `identity_id`, `roles`, and `permissions`.
During V1 compatibility they retain `domain`, `type`, `identifier`, `role`,
and `scopes`. Every protected API request checks the persisted `jti`;
logout revokes its access/refresh family.

## Configuration and verification

See [env-variables.md](./env-variables.md). PostgreSQL uses the shared
`pgxpool` configured by `DB_MAX_CONNS`, `DB_MIN_CONNS`, and
`DB_MAX_CONN_LIFETIME_MIN`.

### Before every commit

1. Run `./scripts/test-coverage.sh`, the same command used by the hook.
2. The gate runs `go test ./...`, requires at least 90% coverage across
   `internal/`, `pkg/`, and `cmd/server`, and rejects coverage, passing-test
   count, or previously passing package regressions against
   `reports/coverage/latest.txt`.
3. Enable the repository hook once per clone with
   `./scripts/setup-githooks.sh` (runs coverage only; bypasses Uber
   `asd-cli` / `ussh` system hooks on commit).

The first green run creates the shared baseline; each later green run updates
it. The hook runs unit tests only and never runs k6.

```bash
./scripts/test-coverage.sh
go build ./...
RATE=200 DURATION=20s ./scripts/loadtest/run-mixed.sh
```

The p95 and p99 comparison and 20% regression limit are defined in
[api-rules.md](./api-rules.md).

## Documentation

- [API and storage reference](./api-docs.md)
- [API route contract](./apis.md)
- [OAuth contract](./OAuth.md) and [OpenAPI](./OAuth.yaml)
- [portalauth claims](./docs/portalauth.md)
- [load testing](./docs/load-testing.md)
- [distributed tracing](./docs/tracing.md)
- [environment variables](./env-variables.md)

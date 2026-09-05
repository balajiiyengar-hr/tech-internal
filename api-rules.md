# API engineering contract

This is the mandatory scale, data-access, and performance contract for every
backend/API change in this repository. Contributors and coding agents must
apply it before calling a change complete.

## Redis

- Pipeline concurrent multi-key or multi-command work wherever possible. Use
  `MGET`, `Pipeline`, or `TxPipeline`; never issue serial `GET` calls in a loop.
- Keep atomic semantics where required. OTP verification deliberately uses one
  `GETDEL`, so no pipeline is useful. If an OTP request starts doing multiple
  Redis operations, queue them in one pipeline.
- Check every command result returned by `Exec`; do not ignore partial failures.

Redis pooling is configured in `internal/database/database.go`. Reuse that
client; do not construct a client per request.

### Authentication working set

- Login and OTP identity reads are Redis-first. Identity metadata uses
  `auth:identity:{type}:{normalized-identifier}` and protected password hashes
  use `auth:pwd:{type}:{normalized-identifier}`. Password plaintext is never
  cached.
- Identity and password reads are sent in one Redis pipeline. Misses are
  singleflight-coalesced, admitted through the bounded `MAX_INFLIGHT_DB`
  semaphore, loaded from Postgres, and filled into Redis.
- Password entries have no TTL. Creates and password changes commit Postgres
  first and write Redis before returning. User deletion, membership deletion,
  and membership updates invalidate every cached identity for the person.
- `auth:lru` is a Redis ZSET of last-access timestamps. The write script trims
  identity/password pairs atomically to `AUTH_CACHE_SIZE` (default 5,000).
- SMS OTPs exist only as AES-GCM-protected Redis values. Request uses `SET` with
  a fixed 120-second expiry and verification atomically consumes with `GETDEL`.

Approximate 5,000-entry memory budget: protected hash 180 B + average identity
metadata 450 B + two key names 140 B + Redis string/dictionary overhead 200 B +
LRU ZSET member/node 150 B + person reverse-index share 120 B = about 1.24 KB
per entry, or 6.2 MB decimal (5.9 MiB). Allowing 20–40% allocator fragmentation
gives a practical **7–8.5 MiB** Redis budget. Actual role/permission and
identifier lengths should be measured with `MEMORY USAGE` in production.

## PostgreSQL

- Connection pooling is mandatory. Repositories receive the shared
  `*pgxpool.Pool` created in `internal/database/database.go`; never open a
  connection per query.
- Tune with `DB_MAX_CONNS` (default 50), `DB_MIN_CONNS` (10), and
  `DB_MAX_CONN_LIFETIME_MIN` (30). Size the database and application replicas
  together so aggregate maximum connections fit the server.
- `MAX_INFLIGHT_DB` defaults to 40, deliberately below the 50-connection pool,
  and is shared by login identity, password, OTP identity, and domain-settings
  cache fallbacks/writes. Do not increase it above the per-instance pool size;
  preserve connections for token and administrative work.
- Every new or changed SQL query needs an `EXPLAIN (ANALYZE, BUFFERS)` check
  against representative data. In review notes, record expected row count,
  index used, observed cost/time, and the load comparison. Queries in this V2
  identity path are supported by organization, identity, membership, role,
  and active-token indexes in `migrations/004_v2_identity.sql`.
- Compare query behavior under load, not only on an empty local database.

## List pagination

- Every `GET` endpoint that returns a collection must paginate at the
  repository/query level. The default page size is 25 and the API hard cap is
  60; values above 60 are clamped to 60.
- Preferred query parameters are `pageSize` and `page`. For compatibility,
  `PageSize` and `PageVal` are also accepted. Both page size and page are
  positive integers; missing, zero, negative, or invalid values use defaults
  of 25 and 1 respectively.
- List responses include `pagination` with `page_size`, `page_val`, `total`,
  and `has_next`. UI clients must send a page size, default to 25, and never
  request more than 60.
- Pagination does not apply to resource-by-ID, health, metrics, or scalar
  configuration endpoints.

## Batch writes and periodic flush

- For many rows, use `COPY`, a multi-value `INSERT`, or `pgx.Batch`; do not send
  one row per network round trip.
- A practical starting point is 100–500 rows or a 100–250 ms flush interval.
  Tune from measurements and payload size.
- Use one transaction per batch. Commit only after all batch results succeed;
  rollback on any error so partial writes and silent loss are impossible.
- A single request that creates one aggregate may use one transaction, but its
  identity/role child rows should still be queued as a batch.

## Performance gate

After any API behavior or hot-path change:

```bash
RATE=200 DURATION=20s ./scripts/loadtest/run-mixed.sh
```

Compare p95 and p99 with `reports/loadtest/mixed_200.txt` and
`reports/loadtest/mixed_200.summary.json`. Compute each regression as:

```text
(new_ms - baseline_ms) / baseline_ms * 100
```

Neither p95 nor p99 may regress by more than 20%. A change is not done until
the gate passes or the PR records an explicit exception with measurements,
cause, risk, and follow-up owner. Preserve the previous report before replacing
baseline artifacts.

## Before every commit

1. Run `./scripts/test-coverage.sh`, the same unit-test command as the hook.
2. The gate requires P0 table tests, at least 90% Go coverage for `internal/`,
   `pkg/`, and `cmd/server`, and no coverage, passing-test-count, or previously
   passing-package regression from `reports/coverage/latest.txt`.
3. Enable it per clone with
   `git config core.hooksPath scripts/githooks` (repository-local config).

The first green run creates the baseline and later green runs update it. The
hook runs no k6 or server process.

## Operational boundaries

- Metrics labels must be low-cardinality (route templates, status classes);
  never label by identifiers, organization IDs, error text, or token IDs.
- New database, cache, and other shared stores must publish low-cardinality
  client latency and pool-utilization metrics on `GET /metrics`.
- Never log passwords, bearer/refresh tokens, encryption keys, or production
  OTPs.
- Authorization is organization-scoped. V2 binds one person and globally
  unique email/phone identity to exactly one organization.
- Protected API calls must retain `jti` revocation checks.
- New APIs must create child spans for outbound database, cache, and HTTP calls.

See [OAuth.md](./OAuth.md) and [docs/portalauth.md](./docs/portalauth.md) for
the authentication and claims contract.

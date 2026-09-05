# Load testing

The mandatory API performance gate is defined in
[api-rules.md](../api-rules.md): run mixed 200 TPS after API changes and keep
both overall p95 and p99 within 20% of the checked-in baseline unless an
explicit review exception is recorded. The mixed script intentionally uses V1
compatibility routes, which remain supported by the V2 identity adapter.

Scripts live in `scripts/loadtest/`. Outputs from recent runs are under `reports/loadtest/`.

Always target the **API** (`http://127.0.0.1:8080/api/v1` on port **8080**), not the UI on **3100**. Requires [k6](https://k6.io/) and the compose stack (`docker compose up -d --build`).

Compose starts Postgres, Redis, API, UI, Prometheus, and Grafana and configures the API with `PASSWORD_KDF=sha256`. The optional `./scripts/run-api.sh` host-process path uses the same KDF unless overridden, but must not run while the compose API is using port **8080**. The Go config default remains `argon2id` if you run `go run ./cmd/server` without that env. Mixed-login throughput numbers below assume sha256 for **new** hashes. The seeded admin `admin@techhr.com` / `Admin@123` is sealed once at first boot; already-sealed hashes are **not** auto-rehashed when you switch `PASSWORD_KDF`.

## Prometheus and Grafana

`docker compose up -d --build` starts Prometheus on `http://127.0.0.1:9090` (scrape interval **1s**, `api:8080/metrics` inside compose) and Grafana on `http://127.0.0.1:3200`. Dashboard **Tech Internal API Observability**, uid `tech-internal-api`:

[http://127.0.0.1:3200/d/tech-internal-api/tech-internal-api-observability](http://127.0.0.1:3200/d/tech-internal-api/tech-internal-api-observability)

Panels: API latency p90 / p95 / p99, Success rate, Error rate, **Server errors 5xx**, **Client errors 4xx**, Request rate, Errors by group, **Traffic by HTTP status class**, p95 latency by route, Traffic by route, Errors by route, Postgres pool connections, Postgres query p95, and Redis command p95. JSON and provisioning: `deploy/grafana/dashboards/api-observability.json`, `deploy/grafana/provisioning/`, `deploy/prometheus/prometheus.yml`.

The API exports `tech_internal_api_http_requests_total` and `tech_internal_api_http_request_duration_seconds` at unauthenticated `GET /metrics`, labeled by Gin route template, method, status class, and stable `error_group` (`not_found`, `none`, `conflict`, … — not request bodies or messages). `/metrics` scrapes are excluded from those series. New shared stores must also expose low-cardinality client latency and pool-utilization metrics.

Keep the dashboard on the last 15 minutes while running:

```bash
docker compose up -d --build
RATE=100 DURATION=20s ./scripts/loadtest/run-mixed.sh
```

`app_delete` in `k6_mixed.js` deletes a nil UUID and **expects HTTP 404**. k6 `http_req_failed` stays 0% when that check passes (`expectedStatuses(404)`). Prometheus still records `error_group="not_found"` on `DELETE /api/v1/admin/apps/:id`. Do not treat that panel as a mixed-test failure.

Because of that, **Error rate** and **Errors by route** are expected to be non-zero during the mixed test — they count every `error_group != "none"`, including the intentional 404 and the duplicate-user `conflict`. Use **Traffic by HTTP status class** and **Server errors 5xx** to separate real server-side failures from expected client errors: 4xx is the load test working as designed, while any sustained 5xx is a genuine API bug.

## Isolated scenarios — `run.sh`

```bash
./scripts/loadtest/run.sh
# or
./scripts/loadtest/run.sh /path/to/out-dir
```

The wrapper:

1. `GET /health`
2. `go test -bench=. -benchmem ./internal/auth` (Argon2id + JWT)
3. Obtains an admin JWT via `POST /auth/login/email`
4. Runs k6 scenarios one at a time: `health_1k`, `apps_1k`, `login_closed`, `login_1k`, `login_ramp`

Single scenario:

```bash
export API_BASE=http://127.0.0.1:8080/api/v1
export ACCESS_TOKEN='<jwt>'
export SCENARIO=health_1k   # health_1k | apps_1k | login_closed | login_1k | login_ramp
k6 run --summary-export /tmp/out.json scripts/loadtest/k6_auth.js
```

k6 summary JSON in this version puts trend stats **directly** on the metric object (`p(95)`, `med`, …), not under `.values`.

## Mixed load — `run-mixed.sh`

Thirteen concurrent `constant-arrival-rate` scenarios in `k6_mixed.js`, each at `RATE` iterations per second for `DURATION`.

```bash
RATE=200 DURATION=20s ./scripts/loadtest/run-mixed.sh
# optional output directory:
RATE=100 DURATION=20s ./scripts/loadtest/run-mixed.sh reports/loadtest
```

| Env | Default | Meaning |
|-----|---------|---------|
| `RATE` | `200` | Arrival rate per scenario (TPS) |
| `DURATION` | `20s` | Measured window |
| `API_BASE` | `http://127.0.0.1:8080/api/v1` | API prefix |
| `SAMPLE_INTERVAL` | `1` | Infra sample period in seconds |

Scenarios (all use the same `RATE`): `health`, `domain`, `login`, `otp_flow`, `me`, `apps`, `admin_domain_get`, `admin_domain_put`, `admin_users`, `user_flow`, `password`, `app_create`, `app_delete`.

k6 `teardown` deletes Load Mix apps and the reserved users through the API. `run-mixed.sh` also installs an `EXIT`/`INT`/`TERM` trap that runs SQL against Docker Postgres so residue is cleaned if k6 is interrupted:

- `portal_apps` where `section = 'Load Mix'` or `name LIKE 'mix-%'`
- V2 `persons`, `organization_memberships`, `login_identities`, and `oauth_tokens` (plus legacy `users`) for reserved identifiers/names: `loadmix@techhr.com`, `+1555%`, `+1999%`, `Load Mix User`, `Load OTP User`, and `mix`

Cleanup after mixed tests is automatic. You do not need a manual purge after a normal run.

### Infra CPU / memory

`run-mixed.sh` wraps k6 with `scripts/loadtest/sample-infra.py`. It writes:

- `reports/loadtest/mixed_${RATE}_infra.csv`
- `reports/loadtest/mixed_${RATE}_infra.summary.json`

Sampling:

- **Postgres** container `tech-internal-db` (host port **5434**) and **Redis** `tech-internal-redis` (**6380**) via `docker stats`
- **Auth API** via `lsof` on TCP listen **8080**, then macOS `ps` `%cpu` and RSS when using `scripts/run-api.sh`. With the compose-first path, sample container `tech-internal-api` via `docker stats`.

Docker CPU% and `ps` `%cpu` are **not** interchangeable. Docker reports container CPU relative to host capacity (can exceed 100% with multiple cores). `ps` `%cpu` is the Go process on the host (percent of one logical CPU; can also exceed 100% when the process uses several cores). Compare postgres/redis only to other docker samples, and the API only to other `ps` samples.

## Microbenchmarks (Apple M4 Max, 16 cores, 2026-09-03)

| Benchmark | ns/op | Notes |
|-----------|-------|--------|
| `BenchmarkArgon2idHash` | ~17.8 ms | `m=19456, t=2, p=1`, ~19 MiB |
| `BenchmarkArgon2idVerify` | ~17.7 ms | Same cost as hash |
| `BenchmarkJWTIssuePair` | ~6.9 µs | Access + refresh HS256 |
| `BenchmarkJWTParse` | ~3.2 µs | HMAC verify |

Email login against an Argon2id-sealed password is Argon2-bound. JWT itself is not the 1000 TPS bottleneck. OTP issue/verify is AES-GCM only (no password KDF on that path).

## Isolated HTTP results (same machine, API `:8080`, 20s unless noted)

These isolated login runs used the seeded admin hash (Argon2id if it was sealed on first boot). k6 **drops** iterations when it cannot schedule the target arrival rate — that is a missed SLO, not necessarily 5xx.

| Scenario | Target | Achieved TPS | p50 | p95 | p99 | Errors | Dropped iters |
|----------|--------|--------------|-----|-----|-----|--------|----------------|
| `GET /health` | 1000 rps | **1000** | 0.20 ms | 0.28 ms | 0.43 ms | 0% | 0 |
| `GET /apps` (Bearer) | 1000 rps | **1000** | 0.66 ms | 1.12 ms | 3.17 ms | 0% | 0 |
| `POST /auth/login/email` closed, 64 VUs | max | **~509** | 86 ms | 361 ms | 555 ms | 0% | n/a |
| `POST /auth/login/email` 1000 rps | 1000 | **~452** | 2.54 s | 2.96 s | 3.16 s | 0%* | **9770** |
| Login ramp to 1000 rps (51s) | 1000 | **~286** | 101 ms | 2.74 s | 3.01 s | 0%* | 3385 |

\*HTTP 200 on completed requests; drops mean the Argon2 login path could not hold 1000 arrivals.

`GET /health` is not access-logged. `/apps` and login are.

## Mixed results on disk (`reports/loadtest/`)

Do not treat aggregate HTTP/s as 13 × `RATE`: two-request flows (OTP, user create/delete) and setup/teardown inflate `http_reqs`.

### Mixed 100 TPS (`mixed_100.txt`, `mixed_100_infra.summary.json`)

`RATE=100`, 20s, 13 scenarios. **0%** `http_req_failed` (0 / 36179), **~1717 HTTP/s**, **26010** complete / **0** interrupted iterations.

Infra while that run was sampled (`docker stats` for DB/Redis, `ps` for the API):

| Target | CPU avg | CPU p95 | CPU max | RSS avg |
|--------|--------:|--------:|--------:|--------:|
| postgres (`tech-internal-db`) | 31.8% | 41.1% | 41.1% | ~93 MiB |
| redis (`tech-internal-redis`) | 1.0% | 1.7% | 1.7% | ~10 MiB |
| auth_api (`ps` on :8080) | 39.6% | 60.7% | 60.7% | ~72 MiB |

Eight samples at ~1s; treat these as a coarse envelope, not a profiler.

### Mixed 200 TPS (`mixed_200.txt`, `mixed_200_infra.summary.json`)

`RATE=200`, 20s (current `k6_mixed.js`, including expected 404 deletes). **0%** `http_req_failed` (0 / 66697), **~3148 HTTP/s**, **52011** complete / **0** interrupted. Grafana/Prometheus will still show `not_found` on the nil-UUID app deletes.

Infra:

| Target | CPU avg | CPU p95 | CPU max | RSS avg |
|--------|--------:|--------:|--------:|--------:|
| postgres (`tech-internal-db`) | 58.1% | 81.5% | 81.5% | ~163 MiB |
| redis (`tech-internal-redis`) | 1.5% | 2.1% | 2.1% | ~10 MiB |
| auth_api (`ps` on :8080) | 66.2% | 81.3% | 81.3% | ~91 MiB |

### Older mixed 200 TPS, sha256 (`mixed_200_sha256.txt`)

Earlier mixed script (colliding test data). `RATE=200`, 20s. **~3057 HTTP/s**, **0 dropped** (52010 complete / 0 interrupted). **~6.55%** `http_req_failed` (4118 / 62786). Failures clustered on colliding test data, not on health/apps/me:

- Login and password-reset share `loadmix@techhr.com`
- OTP Redis keys are `otp:{domain}:{mobile}` with mobiles derived from `__VU`; `__VU` is global across concurrent scenarios
- Apps use unique names `mix-${__VU}-${__ITER}` under section `Load Mix` (unique `(domain, name)` can still 409 under overlap)

Custom error rates from that file: login/password ~24%, OTP request/verify ~23%, app create ~9%; other measured APIs 0%.

### Historical contrast

Isolated Argon2id login at 1000 rps (table above) saturates well below target and **drops** thousands of iterations. Mixed sha256 at 100–200 TPS per API stays in the millisecond range. Prefer `mixed_200.txt` (0% k6 fail) over `mixed_200_sha256.txt` for current-script behavior.

`PASSWORD_KDF=sha256` is a random 16-byte salt plus SHA-256, then AES-256-GCM at rest. Use it for high-throughput trusted/local tests. Keep `argon2id` for internet-facing password databases. MD5 is not supported.

## Auth server implications

Other UIs that only **parse** JWTs via `pkg/portalauth` scale like `/apps` (cheap HMAC). They must not call this API on every request. The expensive path is **issuing** tokens after password verify when the stored hash is Argon2id.

### 500 TPS mixed bottleneck and cache plan

The pre-cache 500 TPS mixed run saturated the roughly 50-connection Postgres
pool. Thousands of request goroutines accumulated in pool acquire and
transaction/advisory-lock waits; raising `DB_MAX_CONNS` toward request
concurrency would only move contention into Postgres.

The auth path now serves domain settings plus identity/password reads from
Redis. A miss is singleflight-coalesced and admitted through one shared
`MAX_INFLIGHT_DB=40` semaphore, leaving headroom in the default
`DB_MAX_CONNS=50` pool for token and admin queries. Redis defaults to a
100-connection pool with 20 warm idle connections. Organization advisory
transaction locks remain only on correctness-sensitive admin membership
update/delete operations; login, password lookup, and OTP paths do not acquire
them.

The password/identity working set is Redis-native LRU, capped at 5,000 entries,
and needs about 7–8.5 MiB including estimated allocator fragmentation (see
`api-rules.md`). Password hashes have no TTL and are DB-first write-through.
SMS OTPs are AES-GCM-protected Redis-only values with an invariant 120-second
TTL and atomic `GETDEL` verification.

After restarting the API, warm the common identities, then rerun:

```bash
RATE=500 DURATION=20s ./scripts/loadtest/run-mixed.sh
```

Compare Postgres pool acquire wait/count, Redis command p95, HTTP p95/p99,
dropped iterations, and 5xx. This implementation does not claim a measured
500 TPS pass until that user-owned API/load-test run is completed.
